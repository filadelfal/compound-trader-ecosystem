package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const runtimeWarning = "PAPER_ONLY_HISTORICAL_RESULTS_DO_NOT_GUARANTEE_FUTURE_PERFORMANCE"

var runtimePairs = map[string]bool{"EURUSD": true, "GBPUSD": true, "USDJPY": true}
var runtimeFrames = map[string]time.Duration{"H1": time.Hour, "H4": 4 * time.Hour}

type runtimeConfig struct {
	Enabled bool
	Mode string
	Pairs []string
	Timeframes []string
	SettlementDelay, ReadinessMaxAge, RecoveryWindow time.Duration
	WorkerLimit, QueueCapacity, MaximumRetries int
	CycleTimeout, InitialRetryDelay, MaximumRetryDelay time.Duration
	LeaderLeaseDuration, LeaderRenewalInterval, ClockSkewTolerance time.Duration
}

func defaultRuntimeConfig() runtimeConfig {
	return runtimeConfig{Mode: "PAPER", Pairs: []string{"EURUSD", "GBPUSD", "USDJPY"}, Timeframes: []string{"H1", "H4"},
		SettlementDelay: 15*time.Second, ReadinessMaxAge: 24*time.Hour, RecoveryWindow: 8*time.Hour,
		WorkerLimit: 2, QueueCapacity: 12, MaximumRetries: 3, CycleTimeout: 30*time.Second,
		InitialRetryDelay: 100*time.Millisecond, MaximumRetryDelay: 2*time.Second,
		LeaderLeaseDuration: 15*time.Second, LeaderRenewalInterval: 5*time.Second, ClockSkewTolerance: 2*time.Second}
}

func (c runtimeConfig) validate() error {
	if c.Mode != "PAPER" { return errors.New("runtime mode must be PAPER") }
	if len(c.Pairs)==0 || len(c.Timeframes)==0 { return errors.New("pair and timeframe allowlists are required") }
	for _, p := range c.Pairs { if !runtimePairs[p] { return fmt.Errorf("unsupported pair %s", p) } }
	for _, f := range c.Timeframes { if runtimeFrames[f]==0 { return fmt.Errorf("unsupported timeframe %s", f) } }
	if c.SettlementDelay < 0 || c.ReadinessMaxAge <= 0 || c.RecoveryWindow <= 0 || c.WorkerLimit < 1 || c.WorkerLimit > 32 ||
		c.QueueCapacity < c.WorkerLimit || c.QueueCapacity > 1024 || c.MaximumRetries < 0 || c.MaximumRetries > 10 ||
		c.CycleTimeout <= 0 || c.InitialRetryDelay <= 0 || c.MaximumRetryDelay < c.InitialRetryDelay ||
		c.LeaderLeaseDuration <= 0 || c.LeaderRenewalInterval <= 0 || c.LeaderRenewalInterval*2 >= c.LeaderLeaseDuration || c.ClockSkewTolerance < 0 {
		return errors.New("invalid conservative runtime limits")
	}
	return nil
}

func (c runtimeConfig) fingerprint() string {
	pairs, frames := append([]string(nil), c.Pairs...), append([]string(nil), c.Timeframes...)
	sort.Strings(pairs); sort.Strings(frames)
	return fingerprint(struct{ Enabled bool; Mode string; Pairs, Frames []string; Settlement, Readiness, Recovery, Cycle, InitialRetry, MaximumRetry, Lease, Renewal, Skew int64; Workers, Queue, Retries int }{
		c.Enabled,c.Mode,pairs,frames,int64(c.SettlementDelay),int64(c.ReadinessMaxAge),int64(c.RecoveryWindow),int64(c.CycleTimeout),int64(c.InitialRetryDelay),int64(c.MaximumRetryDelay),int64(c.LeaderLeaseDuration),int64(c.LeaderRenewalInterval),int64(c.ClockSkewTolerance),c.WorkerLimit,c.QueueCapacity,c.MaximumRetries})
}

func parseRuntimeConfig(get func(string) string) (runtimeConfig, error) {
	c := defaultRuntimeConfig()
	if v:=get("PAPER_AUTOMATION_ENABLED"); v!="" { b,e:=strconv.ParseBool(v); if e!=nil{return c,e}; c.Enabled=b }
	if v:=get("PAPER_AUTOMATION_MODE"); v!="" { c.Mode=v }
	if v:=get("PAPER_AUTOMATION_PAIRS"); v!="" { c.Pairs=splitAllowlist(v) }
	if v:=get("PAPER_AUTOMATION_TIMEFRAMES"); v!="" { c.Timeframes=splitAllowlist(v) }
	durations:=map[string]*time.Duration{"PAPER_AUTOMATION_SETTLEMENT_DELAY":&c.SettlementDelay,"PAPER_AUTOMATION_READINESS_MAX_AGE":&c.ReadinessMaxAge,"PAPER_AUTOMATION_RECOVERY_WINDOW":&c.RecoveryWindow,"PAPER_AUTOMATION_CYCLE_TIMEOUT":&c.CycleTimeout,"PAPER_AUTOMATION_INITIAL_RETRY_DELAY":&c.InitialRetryDelay,"PAPER_AUTOMATION_MAX_RETRY_DELAY":&c.MaximumRetryDelay,"PAPER_AUTOMATION_LEADER_LEASE":&c.LeaderLeaseDuration,"PAPER_AUTOMATION_LEADER_RENEWAL":&c.LeaderRenewalInterval,"PAPER_AUTOMATION_CLOCK_SKEW":&c.ClockSkewTolerance}
	for k,p:=range durations { if v:=get(k);v!="" { d,e:=time.ParseDuration(v);if e!=nil{return c,fmt.Errorf("%s: %w",k,e)};*p=d } }
	ints:=map[string]*int{"PAPER_AUTOMATION_WORKERS":&c.WorkerLimit,"PAPER_AUTOMATION_QUEUE_CAPACITY":&c.QueueCapacity,"PAPER_AUTOMATION_MAX_RETRIES":&c.MaximumRetries}
	for k,p:=range ints { if v:=get(k);v!="" { n,e:=strconv.Atoi(v);if e!=nil{return c,fmt.Errorf("%s: %w",k,e)};*p=n } }
	return c,c.validate()
}
func splitAllowlist(s string) []string { out:=[]string{};for _,v:=range strings.Split(s,","){v=strings.ToUpper(strings.TrimSpace(v));if v!=""{out=append(out,v)}};return out }

type runtimeCycle struct { ID, Pair, Timeframe string; CandleClose, ScheduledAt time.Time; ConfigFingerprint, StrategySetVersion string }
func newRuntimeCycle(pair, frame string, close time.Time, strategyVersion, configFP string) runtimeCycle {
	identity:=struct{Pair,Timeframe,Close,StrategyVersion,Config string}{pair,frame,close.UTC().Format(time.RFC3339Nano),strategyVersion,configFP}
	return runtimeCycle{ID:"paper-cycle-"+fingerprint(identity),Pair:pair,Timeframe:frame,CandleClose:close.UTC(),ScheduledAt:close.UTC(),ConfigFingerprint:configFP,StrategySetVersion:strategyVersion}
}
func latestClosedBoundary(now time.Time, frame time.Duration, settlement time.Duration) time.Time {
	eligible:=now.UTC().Add(-settlement); seconds:=eligible.Unix(); step:=int64(frame/time.Second)
	return time.Unix(seconds-seconds%step,0).UTC()
}
func nextRuntimeBoundary(now time.Time, frame time.Duration, settlement time.Duration) time.Time {
	close:=latestClosedBoundary(now,frame,settlement); next:=close.Add(frame).Add(settlement)
	if !next.After(now) { next=next.Add(frame) }; return next
}

type cycleRecord struct { Cycle runtimeCycle `json:"cycle"`; State string `json:"state"`; Outcome string `json:"outcome"`; Reasons []string `json:"reasons,omitempty"`; Owner string `json:"owner"`; Fence uint64 `json:"fence"`; RequestFingerprint string `json:"requestFingerprint,omitempty"`; ResultFingerprint string `json:"resultFingerprint,omitempty"`; Attempts int `json:"attempts"`; StartedAt *time.Time `json:"startedAt,omitempty"`; CompletedAt *time.Time `json:"completedAt,omitempty"` }
type runtimeStore interface { AcquireLeader(context.Context,string,time.Duration)(uint64,bool,error); RenewLeader(context.Context,string,uint64,time.Duration)(bool,error); AcquireCycle(context.Context,runtimeCycle,string,uint64,time.Duration)(bool,error); Verify(context.Context,string,uint64,runtimeCycle)(bool,error); Load(context.Context,string)(cycleRecord,error); Save(context.Context,cycleRecord)error; Recent(context.Context,int)([]cycleRecord,error); Healthy(context.Context)error }

type redisRuntimeStore struct{ client *redis.Client; prefix string }
func (s redisRuntimeStore) key(v string)string{return s.prefix+v}
func (s redisRuntimeStore) AcquireLeader(ctx context.Context,owner string,ttl time.Duration)(uint64,bool,error){
	const script=`local v=redis.call('get',KEYS[1]); if v then return {0,0} end; local f=redis.call('incr',KEYS[2]); redis.call('psetex',KEYS[1],ARGV[2],ARGV[1]..':'..f); return {f,1}`
	r,e:=s.client.Eval(ctx,script,[]string{s.key("leader"),s.key("fence")},owner,ttl.Milliseconds()).Slice();if e!=nil{return 0,false,e};return uint64(r[0].(int64)),r[1].(int64)==1,nil
}
func (s redisRuntimeStore) RenewLeader(ctx context.Context,owner string,f uint64,ttl time.Duration)(bool,error){const script=`if redis.call('get',KEYS[1])==ARGV[1] then redis.call('pexpire',KEYS[1],ARGV[2]); return 1 else return 0 end`;n,e:=s.client.Eval(ctx,script,[]string{s.key("leader")},fmt.Sprintf("%s:%d",owner,f),ttl.Milliseconds()).Int();return n==1,e}
func (s redisRuntimeStore) AcquireCycle(ctx context.Context,c runtimeCycle,owner string,f uint64,ttl time.Duration)(bool,error){ok,e:=s.client.SetNX(ctx,s.key("lock:"+c.ID),fmt.Sprintf("%s:%d",owner,f),ttl).Result();return ok,e}
func (s redisRuntimeStore) Verify(ctx context.Context,owner string,f uint64,c runtimeCycle)(bool,error){l,e:=s.client.Get(ctx,s.key("leader")).Result();if e!=nil{return false,e};k,e:=s.client.Get(ctx,s.key("lock:"+c.ID)).Result();return e==nil&&l==fmt.Sprintf("%s:%d",owner,f)&&k==l,e}
func (s redisRuntimeStore) Load(ctx context.Context,id string)(cycleRecord,error){b,e:=s.client.Get(ctx,s.key("cycle:"+id)).Bytes();if e!=nil{return cycleRecord{},e};var r cycleRecord;e=json.Unmarshal(b,&r);return r,e}
func (s redisRuntimeStore) Save(ctx context.Context,r cycleRecord)error{b,e:=json.Marshal(r);if e!=nil{return e};p:=s.client.Pipeline();p.Set(ctx,s.key("cycle:"+r.Cycle.ID),b,30*24*time.Hour);p.LPush(ctx,s.key("recent"),b);p.LTrim(ctx,s.key("recent"),0,99);_,e=p.Exec(ctx);return e}
func (s redisRuntimeStore) Recent(ctx context.Context,n int)([]cycleRecord,error){values,e:=s.client.LRange(ctx,s.key("recent"),0,int64(n-1)).Result();if e!=nil{return nil,e};out:=make([]cycleRecord,0,len(values));for _,v:=range values{var r cycleRecord;if json.Unmarshal([]byte(v),&r)==nil{out=append(out,r)}};return out,nil}
func (s redisRuntimeStore) Healthy(ctx context.Context)error{return s.client.Ping(ctx).Err()}

type runtimeRequestProvider interface { Request(context.Context,runtimeCycle)(paperAutomationRequest,error) }
type httpRuntimeRequestProvider struct { endpoint string; client *http.Client }
func (p httpRuntimeRequestProvider) Request(ctx context.Context,c runtimeCycle)(paperAutomationRequest,error){
	u,e:=url.Parse(p.endpoint);if e!=nil{return paperAutomationRequest{},e};q:=u.Query();q.Set("cycleId",c.ID);q.Set("pair",c.Pair);q.Set("timeframe",c.Timeframe);q.Set("candleClose",c.CandleClose.Format(time.RFC3339Nano));u.RawQuery=q.Encode()
	req,e:=http.NewRequestWithContext(ctx,http.MethodGet,u.String(),nil);if e!=nil{return paperAutomationRequest{},e};response,e:=p.client.Do(req);if e!=nil{return paperAutomationRequest{},e};defer response.Body.Close();if response.StatusCode!=http.StatusOK{return paperAutomationRequest{},fmt.Errorf("request provider returned %d",response.StatusCode)};decoder:=json.NewDecoder(io.LimitReader(response.Body,1<<20));decoder.DisallowUnknownFields();var out paperAutomationRequest;if e=decoder.Decode(&out);e!=nil{return out,e};if out.RequestID!=c.ID||out.Mode!="PAPER"||len(out.StrategyManager.Setups)!=1||out.StrategyManager.Setups[0].Pair!=c.Pair||out.StrategyManager.Setups[0].Timeframe!=c.Timeframe{return out,errors.New("provider identity conflict")};return out,nil
}
type runtimeStatus struct { Enabled bool `json:"enabled"`; Live bool `json:"live"`; Ready bool `json:"ready"`; Paused bool `json:"paused"`; Leader bool `json:"leader"`; State string `json:"state"`; Reasons []string `json:"reasons,omitempty"`; OwnerID string `json:"ownerId"`; Fence uint64 `json:"fencingToken"`; QueueDepth int `json:"queueDepth"`; ConfigFingerprint string `json:"configurationFingerprint"`; LastSuccess map[string]time.Time `json:"lastSuccess"`; Warning string `json:"warning"` }
type paperRuntime struct { cfg runtimeConfig; app *application; store runtimeStore; provider runtimeRequestProvider; owner string; now func()time.Time; queue chan runtimeCycle; cancel context.CancelFunc; wg sync.WaitGroup; mu sync.RWMutex; status runtimeStatus; stopped atomic.Bool }

func newPaperRuntime(cfg runtimeConfig,app *application,store runtimeStore,provider runtimeRequestProvider)(*paperRuntime,error){if e:=cfg.validate();e!=nil{return nil,e};if app==nil{return nil,errors.New("Milestone D coordinator is required")};b:=make([]byte,16);if _,e:=rand.Read(b);e!=nil{return nil,e};r:=&paperRuntime{cfg:cfg,app:app,store:store,provider:provider,owner:hex.EncodeToString(b),now:app.currentTime,queue:make(chan runtimeCycle,cfg.QueueCapacity)};r.status=runtimeStatus{Enabled:cfg.Enabled,State:"disabled",OwnerID:r.owner,ConfigFingerprint:cfg.fingerprint(),LastSuccess:map[string]time.Time{},Warning:runtimeWarning};return r,nil}
func (r *paperRuntime) Start(parent context.Context){if !r.cfg.Enabled{return};ctx,cancel:=context.WithCancel(parent);r.cancel=cancel;r.setState("standby",false,"NOT_LEADER");r.wg.Add(1);go r.leaderLoop(ctx);for i:=0;i<r.cfg.WorkerLimit;i++{r.wg.Add(1);go r.worker(ctx)}}
func (r *paperRuntime) Stop(ctx context.Context)error{r.stopped.Store(true);if r.cancel!=nil{r.cancel()};done:=make(chan struct{});go func(){r.wg.Wait();close(done)}();select{case<-done:r.setState("paused",false,"SHUTTING_DOWN");return nil;case<-ctx.Done():return ctx.Err()}}
func (r *paperRuntime) setState(state string,leader bool,reasons ...string){r.mu.Lock();defer r.mu.Unlock();r.status.State=state;r.status.Leader=leader;r.status.Live=!r.stopped.Load();r.status.Ready=state=="leader";r.status.Reasons=append([]string(nil),reasons...);r.status.QueueDepth=len(r.queue)}
func (r *paperRuntime) Status()runtimeStatus{r.mu.RLock();defer r.mu.RUnlock();s:=r.status;s.Reasons=append([]string(nil),s.Reasons...);s.LastSuccess=map[string]time.Time{};for k,v:=range r.status.LastSuccess{s.LastSuccess[k]=v};s.QueueDepth=len(r.queue);return s}
func (r *paperRuntime) leaderLoop(ctx context.Context){defer r.wg.Done();tick:=time.NewTicker(r.cfg.LeaderRenewalInterval);defer tick.Stop();var fence uint64;for{select{case<-ctx.Done():return;case<-tick.C:if fence==0{f,ok,e:=r.store.AcquireLeader(ctx,r.owner,r.cfg.LeaderLeaseDuration);if e!=nil{r.setState("degraded",false,"LEASE_UNAVAILABLE");continue};if !ok{r.setState("standby",false,"NOT_LEADER");continue};fence=f;r.mu.Lock();r.status.Fence=f;r.mu.Unlock();r.setState("leader",true);r.schedule(r.now(),f)}else{ok,e:=r.store.RenewLeader(ctx,r.owner,fence,r.cfg.LeaderLeaseDuration);if e!=nil||!ok{fence=0;r.setState("degraded",false,"LEASE_LOST");continue};r.schedule(r.now(),fence)}}}}
func (r *paperRuntime) schedule(now time.Time,fence uint64){for _,p:=range r.cfg.Pairs{for _,f:=range r.cfg.Timeframes{close:=latestClosedBoundary(now,runtimeFrames[f],r.cfg.SettlementDelay);c:=newRuntimeCycle(p,f,close,"strategy-set-v1",r.cfg.fingerprint());select{case r.queue<-c:default:r.setState("degraded",true,"QUEUE_FULL","BACKPRESSURE")}}}}
func (r *paperRuntime) worker(ctx context.Context){defer r.wg.Done();for{select{case<-ctx.Done():return;case c:=<-r.queue:r.runCycle(ctx,c)}}}
func (r *paperRuntime) runCycle(parent context.Context,c runtimeCycle){s:=r.Status();if !s.Leader{return};ctx,cancel:=context.WithTimeout(parent,r.cfg.CycleTimeout);defer cancel();locked,e:=r.store.AcquireCycle(ctx,c,r.owner,s.Fence,r.cfg.CycleTimeout+r.cfg.MaximumRetryDelay);if e!=nil||!locked{return};if old,e:=r.store.Load(ctx,c.ID);e==nil&&(old.State=="COMPLETED"||old.State=="NO_TRADE"||old.State=="PAPER_ACCEPTED"){return};now:=r.now().UTC();rec:=cycleRecord{Cycle:c,State:"EVALUATING",Owner:r.owner,Fence:s.Fence,StartedAt:&now};_ = r.store.Save(ctx,rec);if r.provider==nil{rec.State="PAUSED";rec.Reasons=[]string{"MARKET_DATA_UNAVAILABLE"};_ = r.store.Save(ctx,rec);r.setState("degraded",true,rec.Reasons...);return};var req paperAutomationRequest;var result paperAutomationResult;for attempt:=0;attempt<=r.cfg.MaximumRetries;attempt++{rec.Attempts=attempt+1;req,e=r.provider.Request(ctx,c);if e==nil{rec.RequestFingerprint=fingerprint(req);result,_=r.app.evaluatePaperAutomation(ctx,req);if result.Outcome=="PAPER_ACCEPTED"||result.Outcome=="IDEMPOTENT_REPLAY"||result.Outcome=="NO_TRADE"{break}};if attempt<r.cfg.MaximumRetries{delay:=r.cfg.InitialRetryDelay<<attempt;if delay>r.cfg.MaximumRetryDelay{delay=r.cfg.MaximumRetryDelay};select{case<-ctx.Done():e=ctx.Err();attempt=r.cfg.MaximumRetries;case<-time.After(delay):}}};verified,ve:=r.store.Verify(ctx,r.owner,s.Fence,c);completed:=r.now().UTC();rec.CompletedAt=&completed;if e!=nil||ve!=nil||!verified{rec.State="FAILED";rec.Outcome="NO_TRADE";rec.Reasons=[]string{"STALE_LEADER"}}else{rec.Outcome=result.Outcome;rec.ResultFingerprint=fingerprint(result);if result.Outcome=="PAPER_ACCEPTED"||result.Outcome=="IDEMPOTENT_REPLAY"{rec.State="COMPLETED";r.mu.Lock();r.status.LastSuccess[c.Pair+"/"+c.Timeframe]=completed;r.mu.Unlock()}else{rec.State="NO_TRADE";rec.Reasons=result.Reasons}};_ = r.store.Save(ctx,rec)}

func (r *paperRuntime) statusHandler(w http.ResponseWriter,q *http.Request){if q.Method!=http.MethodGet{jsonResponse(w,http.StatusMethodNotAllowed,map[string]any{"error":"method_not_allowed"});return};jsonResponse(w,http.StatusOK,map[string]any{"runtime":r.Status()})}
func (r *paperRuntime) cyclesHandler(w http.ResponseWriter,q *http.Request){if q.Method!=http.MethodGet{jsonResponse(w,http.StatusMethodNotAllowed,map[string]any{"error":"method_not_allowed"});return};items,e:=r.store.Recent(q.Context(),50);if e!=nil{jsonResponse(w,http.StatusServiceUnavailable,map[string]any{"error":"STORAGE_UNAVAILABLE"});return};jsonResponse(w,http.StatusOK,map[string]any{"cycles":items,"warning":runtimeWarning})}

func envRuntimeConfig()(runtimeConfig,error){return parseRuntimeConfig(os.Getenv)}
func envRuntimeProvider(cfg runtimeConfig)(runtimeRequestProvider,error){endpoint:=strings.TrimSpace(os.Getenv("PAPER_AUTOMATION_REQUEST_URL"));if !cfg.Enabled{return nil,nil};if endpoint==""{return nil,errors.New("PAPER_AUTOMATION_REQUEST_URL is required when enabled")};u,e:=url.Parse(endpoint);if e!=nil||u.Scheme!="http"&&u.Scheme!="https"||u.Host==""{return nil,errors.New("invalid PAPER_AUTOMATION_REQUEST_URL")};return httpRuntimeRequestProvider{endpoint:endpoint,client:&http.Client{Timeout:cfg.CycleTimeout}},nil}
