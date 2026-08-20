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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

const runtimeWarning = "PAPER_ONLY_HISTORICAL_RESULTS_DO_NOT_GUARANTEE_FUTURE_PERFORMANCE"

var runtimePairs = map[string]bool{"EURUSD": true, "GBPUSD": true, "USDJPY": true}
var runtimeFrames = map[string]time.Duration{"H1": time.Hour, "H4": 4 * time.Hour}
var runtimeReasons = map[string]bool{"NONE":true,"RUNTIME_DISABLED":true,"RUNTIME_PAUSED":true,"NOT_LEADER":true,"LEASE_UNAVAILABLE":true,"LEASE_LOST":true,"STALE_LEADER":true,"CYCLE_LOCKED":true,"PAIR_LOCKED":true,"CYCLE_ALREADY_COMPLETED":true,"CYCLE_STATE_CONFLICT":true,"MISSED_CYCLE_EXPIRED":true,"OPEN_CANDLE":true,"SETTLEMENT_DELAY":true,"OUTSIDE_SESSION":true,"MARKET_DATA_UNAVAILABLE":true,"STALE_MARKET_DATA":true,"STALE_QUOTE":true,"READINESS_EXPIRED":true,"KILL_SWITCH_ACTIVE":true,"TRADING_DISABLED":true,"STORAGE_UNAVAILABLE":true,"CLOCK_SKEW":true,"QUEUE_FULL":true,"BACKPRESSURE":true,"CYCLE_TIMEOUT":true,"RETRY_EXHAUSTED":true,"SHUTTING_DOWN":true,"NO_TRADE":true,"PAPER_ACCEPTED":true}
var runtimeEvents = prometheus.NewCounterVec(prometheus.CounterOpts{Name:"paper_automation_runtime_events_total",Help:"Bounded PAPER automation runtime events."},[]string{"event","reason","pair","timeframe"})
var runtimeQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{Name:"paper_automation_runtime_queue_depth",Help:"Current bounded runtime queue depth."})
var runtimeLeader = prometheus.NewGauge(prometheus.GaugeOpts{Name:"paper_automation_runtime_leader",Help:"One when this replica is scheduler leader."})
var runtimeCycleDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name:"paper_automation_runtime_cycle_duration_seconds",Help:"PAPER runtime cycle duration.",Buckets:prometheus.DefBuckets},[]string{"pair","timeframe","outcome"})
func init(){prometheus.MustRegister(runtimeEvents,runtimeQueueDepth,runtimeLeader,runtimeCycleDuration)}
func observeRuntime(event,reason,pair,frame string){if !runtimeReasons[reason]{reason="OTHER"};if !runtimePairs[pair]{pair="NONE"};if runtimeFrames[frame]==0{frame="NONE"};runtimeEvents.WithLabelValues(event,reason,pair,frame).Inc()}

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
	return runtimeCycle{ID:"paper-cycle-"+strings.TrimPrefix(fingerprint(identity),"sha256:"),Pair:pair,Timeframe:frame,CandleClose:close.UTC(),ScheduledAt:close.UTC(),ConfigFingerprint:configFP,StrategySetVersion:strategyVersion}
}
func latestClosedBoundary(now time.Time, frame time.Duration, settlement time.Duration) time.Time {
	eligible:=now.UTC().Add(-settlement); seconds:=eligible.Unix(); step:=int64(frame/time.Second)
	return time.Unix(seconds-seconds%step,0).UTC()
}
func nextRuntimeBoundary(now time.Time, frame time.Duration, settlement time.Duration) time.Time {
	close:=latestClosedBoundary(now,frame,settlement); next:=close.Add(frame).Add(settlement)
	if !next.After(now) { next=next.Add(frame) }; return next
}
func retryDelay(cycleID string,attempt int,initial,maximum time.Duration)time.Duration{delay:=initial<<attempt;if delay>maximum{delay=maximum};sum:=fingerprint(struct{ID string;Attempt int}{cycleID,attempt});raw,_:=hex.DecodeString(strings.TrimPrefix(sum,"sha256:")[:2]);jitterLimit:=delay/4;if jitterLimit>0&&len(raw)==1{delay+=time.Duration(int64(raw[0])%int64(jitterLimit+1))};if delay>maximum{delay=maximum};return delay}

type cycleRecord struct { Cycle runtimeCycle `json:"cycle"`; State string `json:"state"`; Outcome string `json:"outcome"`; Reasons []string `json:"reasons,omitempty"`; Owner string `json:"owner"`; Fence uint64 `json:"fence"`; RequestFingerprint string `json:"requestFingerprint,omitempty"`; ResultFingerprint string `json:"resultFingerprint,omitempty"`; Attempts int `json:"attempts"`; StartedAt *time.Time `json:"startedAt,omitempty"`; CompletedAt *time.Time `json:"completedAt,omitempty"` }
type runtimeStore interface { AcquireLeader(context.Context,string,time.Duration)(uint64,bool,error); RenewLeader(context.Context,string,uint64,time.Duration)(bool,error); AcquireCycle(context.Context,runtimeCycle,string,uint64,time.Duration)(bool,error); AcquirePair(context.Context,runtimeCycle,string,uint64,time.Duration)(bool,error); Verify(context.Context,string,uint64,runtimeCycle)(bool,error); Release(context.Context,runtimeCycle,string,uint64)error; Load(context.Context,string)(cycleRecord,error); Save(context.Context,cycleRecord)error; Recent(context.Context,int)([]cycleRecord,error); Healthy(context.Context)error; ServerTime(context.Context)(time.Time,error) }

type redisRuntimeStore struct{ client *redis.Client; prefix string }
func (s redisRuntimeStore) key(v string)string{return s.prefix+v}
func (s redisRuntimeStore) AcquireLeader(ctx context.Context,owner string,ttl time.Duration)(uint64,bool,error){
	const script=`local v=redis.call('get',KEYS[1]); if v then return {0,0} end; local f=redis.call('incr',KEYS[2]); redis.call('psetex',KEYS[1],ARGV[2],ARGV[1]..':'..f); return {f,1}`
	r,e:=s.client.Eval(ctx,script,[]string{s.key("leader"),s.key("fence")},owner,ttl.Milliseconds()).Slice();if e!=nil{return 0,false,e};return uint64(r[0].(int64)),r[1].(int64)==1,nil
}
func (s redisRuntimeStore) RenewLeader(ctx context.Context,owner string,f uint64,ttl time.Duration)(bool,error){const script=`if redis.call('get',KEYS[1])==ARGV[1] then redis.call('pexpire',KEYS[1],ARGV[2]); return 1 else return 0 end`;n,e:=s.client.Eval(ctx,script,[]string{s.key("leader")},fmt.Sprintf("%s:%d",owner,f),ttl.Milliseconds()).Int();return n==1,e}
func (s redisRuntimeStore) AcquireCycle(ctx context.Context,c runtimeCycle,owner string,f uint64,ttl time.Duration)(bool,error){ok,e:=s.client.SetNX(ctx,s.key("lock:"+c.ID),fmt.Sprintf("%s:%d",owner,f),ttl).Result();return ok,e}
func (s redisRuntimeStore) AcquirePair(ctx context.Context,c runtimeCycle,owner string,f uint64,ttl time.Duration)(bool,error){ok,e:=s.client.SetNX(ctx,s.key("pair:"+c.Pair),fmt.Sprintf("%s:%d",owner,f),ttl).Result();return ok,e}
func (s redisRuntimeStore) Verify(ctx context.Context,owner string,f uint64,c runtimeCycle)(bool,error){expected:=fmt.Sprintf("%s:%d",owner,f);values,e:=s.client.MGet(ctx,s.key("leader"),s.key("lock:"+c.ID),s.key("pair:"+c.Pair)).Result();if e!=nil||len(values)!=3{return false,e};return values[0]==expected&&values[1]==expected&&values[2]==expected,nil}
func (s redisRuntimeStore) Release(ctx context.Context,c runtimeCycle,owner string,f uint64)error{const script=`if redis.call('get',KEYS[1])==ARGV[1] then redis.call('del',KEYS[1]) end; if redis.call('get',KEYS[2])==ARGV[1] then redis.call('del',KEYS[2]) end; return 1`;return s.client.Eval(ctx,script,[]string{s.key("lock:"+c.ID),s.key("pair:"+c.Pair)},fmt.Sprintf("%s:%d",owner,f)).Err()}
func (s redisRuntimeStore) Load(ctx context.Context,id string)(cycleRecord,error){b,e:=s.client.Get(ctx,s.key("cycle:"+id)).Bytes();if e!=nil{return cycleRecord{},e};var r cycleRecord;e=json.Unmarshal(b,&r);return r,e}
func (s redisRuntimeStore) Save(ctx context.Context,r cycleRecord)error{b,e:=json.Marshal(r);if e!=nil{return e};p:=s.client.Pipeline();p.Set(ctx,s.key("cycle:"+r.Cycle.ID),b,30*24*time.Hour);p.RPush(ctx,s.key("history:"+r.Cycle.ID),b);p.Expire(ctx,s.key("history:"+r.Cycle.ID),30*24*time.Hour);p.LPush(ctx,s.key("recent"),b);p.LTrim(ctx,s.key("recent"),0,99);_,e=p.Exec(ctx);return e}
func (s redisRuntimeStore) Recent(ctx context.Context,n int)([]cycleRecord,error){values,e:=s.client.LRange(ctx,s.key("recent"),0,int64(n-1)).Result();if e!=nil{return nil,e};out:=make([]cycleRecord,0,len(values));for _,v:=range values{var r cycleRecord;if json.Unmarshal([]byte(v),&r)==nil{out=append(out,r)}};return out,nil}
func (s redisRuntimeStore) Healthy(ctx context.Context)error{return s.client.Ping(ctx).Err()}
func (s redisRuntimeStore) ServerTime(ctx context.Context)(time.Time,error){return s.client.Time(ctx).Result()}

type runtimeRequestProvider interface { Request(context.Context,runtimeCycle)(paperAutomationRequest,error) }
type permanentRuntimeError struct{ reason string }
func(e permanentRuntimeError)Error()string{return e.reason}
type httpRuntimeRequestProvider struct { endpoint string; client *http.Client }
func (p httpRuntimeRequestProvider) Request(ctx context.Context,c runtimeCycle)(paperAutomationRequest,error){
	u,e:=url.Parse(p.endpoint);if e!=nil{return paperAutomationRequest{},e};q:=u.Query();q.Set("cycleId",c.ID);q.Set("pair",c.Pair);q.Set("timeframe",c.Timeframe);q.Set("candleClose",c.CandleClose.Format(time.RFC3339Nano));u.RawQuery=q.Encode()
	req,e:=http.NewRequestWithContext(ctx,http.MethodGet,u.String(),nil);if e!=nil{return paperAutomationRequest{},e};response,e:=p.client.Do(req);if e!=nil{return paperAutomationRequest{},e};defer response.Body.Close();if response.StatusCode!=http.StatusOK{if response.StatusCode>=400&&response.StatusCode<500{return paperAutomationRequest{},permanentRuntimeError{"MARKET_DATA_UNAVAILABLE"}};return paperAutomationRequest{},fmt.Errorf("request provider returned %d",response.StatusCode)};decoder:=json.NewDecoder(io.LimitReader(response.Body,1<<20));decoder.DisallowUnknownFields();var out paperAutomationRequest;if e=decoder.Decode(&out);e!=nil{return out,permanentRuntimeError{"MARKET_DATA_UNAVAILABLE"}};if out.RequestID!=c.ID||out.Mode!="PAPER"||len(out.StrategyManager.Setups)!=1||out.StrategyManager.Setups[0].Pair!=c.Pair||out.StrategyManager.Setups[0].Timeframe!=c.Timeframe{return out,permanentRuntimeError{"CYCLE_STATE_CONFLICT"}};if out.StrategyManager.Setups[0].Strategy=="LONDON_BREAKOUT"&&(c.CandleClose.Hour()<9||c.CandleClose.Hour()>11){return out,permanentRuntimeError{"OUTSIDE_SESSION"}};return out,nil
}
type runtimeStatus struct { Enabled bool `json:"enabled"`; Live bool `json:"live"`; Ready bool `json:"ready"`; Paused bool `json:"paused"`; Leader bool `json:"leader"`; State string `json:"state"`; Reasons []string `json:"reasons,omitempty"`; Dependencies map[string]string `json:"dependencies"`; OwnerID string `json:"ownerId"`; Fence uint64 `json:"fencingToken"`; QueueDepth int `json:"queueDepth"`; ConfigFingerprint string `json:"configurationFingerprint"`; LastSuccess map[string]time.Time `json:"lastSuccess"`; Warning string `json:"warning"` }
type runtimeFence struct{ cycle runtimeCycle; token uint64 }
type paperRuntime struct { cfg runtimeConfig; app *application; store runtimeStore; provider runtimeRequestProvider; owner string; now func()time.Time; queue chan runtimeCycle; cancel context.CancelFunc; wg sync.WaitGroup; mu sync.RWMutex; status runtimeStatus; lastScheduled map[string]time.Time; active map[string]runtimeFence; stopped atomic.Bool }

func newPaperRuntime(cfg runtimeConfig,app *application,store runtimeStore,provider runtimeRequestProvider)(*paperRuntime,error){if e:=cfg.validate();e!=nil{return nil,e};if app==nil{return nil,errors.New("Milestone D coordinator is required")};if store==nil{return nil,errors.New("runtime store is required")};b:=make([]byte,16);if _,e:=rand.Read(b);e!=nil{return nil,e};r:=&paperRuntime{cfg:cfg,app:app,store:store,provider:provider,owner:hex.EncodeToString(b),now:app.currentTime,queue:make(chan runtimeCycle,cfg.QueueCapacity),lastScheduled:map[string]time.Time{},active:map[string]runtimeFence{}};r.status=runtimeStatus{Enabled:cfg.Enabled,State:"disabled",Dependencies:map[string]string{"redis":"unknown","requestProvider":"unknown"},OwnerID:r.owner,ConfigFingerprint:cfg.fingerprint(),LastSuccess:map[string]time.Time{},Warning:runtimeWarning};if !cfg.Enabled{r.status.Dependencies=map[string]string{"redis":"not_required","requestProvider":"not_required"}}else{app.automationAcceptanceGate=r.acceptanceGate};return r,nil}
func(r *paperRuntime)acceptanceGate(ctx context.Context,request paperAutomationRequest)string{r.mu.RLock();active,ok:=r.active[request.RequestID];leader:=r.status.Leader&&r.status.Fence==active.token;r.mu.RUnlock();if !ok||!leader{return "STALE_LEADER"};verified,e:=r.store.Verify(ctx,r.owner,active.token,active.cycle);if e!=nil{return "STORAGE_UNAVAILABLE"};if !verified{return "STALE_LEADER"};return ""}
func(r *paperRuntime)setActive(id string,c runtimeCycle,token uint64){r.mu.Lock();r.active[id]=runtimeFence{c,token};r.mu.Unlock()}
func(r *paperRuntime)clearActive(id string){r.mu.Lock();delete(r.active,id);r.mu.Unlock()}
func (r *paperRuntime) Start(parent context.Context){if !r.cfg.Enabled{return};ctx,cancel:=context.WithCancel(parent);r.cancel=cancel;r.setState("standby",false,"NOT_LEADER");r.wg.Add(1);go r.leaderLoop(ctx);for i:=0;i<r.cfg.WorkerLimit;i++{r.wg.Add(1);go r.worker(ctx)}}
func (r *paperRuntime) Stop(ctx context.Context)error{r.stopped.Store(true);if r.cancel!=nil{r.cancel()};done:=make(chan struct{});go func(){r.wg.Wait();close(done)}();select{case<-done:r.setState("paused",false,"SHUTTING_DOWN");return nil;case<-ctx.Done():return ctx.Err()}}
func (r *paperRuntime) setState(state string,leader bool,reasons ...string){r.mu.Lock();defer r.mu.Unlock();r.status.State=state;r.status.Leader=leader;r.status.Live=!r.stopped.Load();r.status.Ready=state=="leader";r.status.Paused=state=="paused"||state=="degraded";r.status.Reasons=append([]string(nil),reasons...);r.status.QueueDepth=len(r.queue);runtimeQueueDepth.Set(float64(len(r.queue)));if leader{runtimeLeader.Set(1)}else{runtimeLeader.Set(0)};reason:="NONE";if len(reasons)>0{reason=reasons[0]};observeRuntime("state",reason,"NONE","NONE")}
func (r *paperRuntime) Status()runtimeStatus{r.mu.RLock();defer r.mu.RUnlock();s:=r.status;s.Reasons=append([]string(nil),s.Reasons...);s.LastSuccess=map[string]time.Time{};for k,v:=range r.status.LastSuccess{s.LastSuccess[k]=v};s.Dependencies=map[string]string{};for k,v:=range r.status.Dependencies{s.Dependencies[k]=v};s.QueueDepth=len(r.queue);return s}
func(r *paperRuntime)setDependency(name,value string){r.mu.Lock();r.status.Dependencies[name]=value;r.mu.Unlock()}
func (r *paperRuntime) leaderLoop(ctx context.Context){defer r.wg.Done();tick:=time.NewTicker(r.cfg.LeaderRenewalInterval);defer tick.Stop();var fence uint64;attempt:=func(){local:=r.now().UTC();server,e:=r.store.ServerTime(ctx);if e!=nil{r.setDependency("redis","unavailable");r.setState("degraded",false,"STORAGE_UNAVAILABLE");return};r.setDependency("redis","available");if r.provider==nil{r.setDependency("requestProvider","unavailable");r.setState("degraded",false,"MARKET_DATA_UNAVAILABLE");return};r.setDependency("requestProvider","available");delta:=local.Sub(server.UTC());if delta<0{delta=-delta};if delta>r.cfg.ClockSkewTolerance{r.setState("degraded",false,"CLOCK_SKEW");return};if fence==0{f,ok,e:=r.store.AcquireLeader(ctx,r.owner,r.cfg.LeaderLeaseDuration);if e!=nil{r.setState("degraded",false,"LEASE_UNAVAILABLE");return};if !ok{r.setState("standby",false,"NOT_LEADER");return};fence=f;r.mu.Lock();r.status.Fence=f;r.mu.Unlock();r.setState("leader",true);r.scheduleRecovery(local,f);return};ok,e:=r.store.RenewLeader(ctx,r.owner,fence,r.cfg.LeaderLeaseDuration);if e!=nil||!ok{fence=0;r.setState("degraded",false,"LEASE_LOST");return};r.schedule(local,fence)};attempt();for{select{case<-ctx.Done():return;case<-tick.C:attempt()}}}
func (r *paperRuntime) enqueue(c runtimeCycle){key:=c.Pair+"/"+c.Timeframe;r.mu.Lock();last:=r.lastScheduled[key];if !last.IsZero()&&!c.CandleClose.After(last){r.mu.Unlock();return};r.lastScheduled[key]=c.CandleClose;r.mu.Unlock();select{case r.queue<-c:observeRuntime("scheduled","NONE",c.Pair,c.Timeframe);runtimeQueueDepth.Set(float64(len(r.queue)));default:r.mu.Lock();delete(r.lastScheduled,key);r.mu.Unlock();observeRuntime("skipped","QUEUE_FULL",c.Pair,c.Timeframe);r.setState("degraded",true,"QUEUE_FULL","BACKPRESSURE")}}
func (r *paperRuntime) schedule(now time.Time,fence uint64){_ = fence;for _,p:=range r.cfg.Pairs{for _,f:=range r.cfg.Timeframes{close:=latestClosedBoundary(now,runtimeFrames[f],r.cfg.SettlementDelay);r.enqueue(newRuntimeCycle(p,f,close,"strategy-set-v1",r.cfg.fingerprint()))}}}
func (r *paperRuntime) recoveryCycles(now time.Time)[]runtimeCycle{cycles:=[]runtimeCycle{};floor:=now.UTC().Add(-r.cfg.RecoveryWindow);for _,p:=range r.cfg.Pairs{for _,f:=range r.cfg.Timeframes{interval:=runtimeFrames[f];latest:=latestClosedBoundary(now,interval,r.cfg.SettlementDelay);first:=latestClosedBoundary(floor.Add(interval),interval,0);for close:=first;!close.After(latest);close=close.Add(interval){if close.Before(floor){continue};cycles=append(cycles,newRuntimeCycle(p,f,close,"strategy-set-v1",r.cfg.fingerprint()))}}};sort.Slice(cycles,func(i,j int)bool{if cycles[i].CandleClose.Equal(cycles[j].CandleClose){if cycles[i].Pair==cycles[j].Pair{return cycles[i].Timeframe<cycles[j].Timeframe};return cycles[i].Pair<cycles[j].Pair};return cycles[i].CandleClose.Before(cycles[j].CandleClose)});return cycles}
func (r *paperRuntime) scheduleRecovery(now time.Time,fence uint64){_ = fence;for _,c:=range r.recoveryCycles(now){r.enqueue(c)}}
func (r *paperRuntime) worker(ctx context.Context){defer r.wg.Done();for{select{case<-ctx.Done():return;case c:=<-r.queue:r.runCycle(ctx,c)}}}
func (r *paperRuntime) runCycle(parent context.Context,c runtimeCycle){startedWall:=time.Now();s:=r.Status();if !s.Leader{observeRuntime("skipped","NOT_LEADER",c.Pair,c.Timeframe);return};ctx,cancel:=context.WithTimeout(parent,r.cfg.CycleTimeout);defer cancel();locked,e:=r.store.AcquireCycle(ctx,c,r.owner,s.Fence,r.cfg.CycleTimeout+r.cfg.MaximumRetryDelay);if e!=nil||!locked{observeRuntime("skipped","CYCLE_LOCKED",c.Pair,c.Timeframe);return};pairLocked,e:=r.store.AcquirePair(ctx,c,r.owner,s.Fence,r.cfg.CycleTimeout+r.cfg.MaximumRetryDelay);if e!=nil||!pairLocked{_ = r.store.Release(ctx,c,r.owner,s.Fence);observeRuntime("skipped","PAIR_LOCKED",c.Pair,c.Timeframe);return};defer r.store.Release(context.Background(),c,r.owner,s.Fence);if old,e:=r.store.Load(ctx,c.ID);e==nil&&(old.State=="COMPLETED"||old.State=="NO_TRADE"||old.State=="PAPER_ACCEPTED"){observeRuntime("skipped","CYCLE_ALREADY_COMPLETED",c.Pair,c.Timeframe);return};now:=r.now().UTC();rec:=cycleRecord{Cycle:c,State:"EVALUATING",Owner:r.owner,Fence:s.Fence,StartedAt:&now};if e=r.store.Save(ctx,rec);e!=nil{r.setState("degraded",true,"STORAGE_UNAVAILABLE");return};if r.provider==nil{rec.State="PAUSED";rec.Reasons=[]string{"MARKET_DATA_UNAVAILABLE"};_ = r.store.Save(ctx,rec);r.setState("degraded",true,rec.Reasons...);return};var req paperAutomationRequest;var result paperAutomationResult;for attempt:=0;attempt<=r.cfg.MaximumRetries;attempt++{rec.Attempts=attempt+1;req,e=r.provider.Request(ctx,c);if e==nil{rec.RequestFingerprint=fingerprint(req);r.setActive(req.RequestID,c,s.Fence);result,_=r.app.evaluatePaperAutomation(ctx,req);r.clearActive(req.RequestID);if result.Outcome=="PAPER_ACCEPTED"||result.Outcome=="IDEMPOTENT_REPLAY"||result.Outcome=="NO_TRADE"{break}}else{var permanent permanentRuntimeError;if errors.As(e,&permanent){result=paperAutomationResult{Outcome:"NO_TRADE",Reasons:[]string{permanent.reason},Warnings:[]string{runtimeWarning}};e=nil;break}};if attempt<r.cfg.MaximumRetries{observeRuntime("retry","NONE",c.Pair,c.Timeframe);select{case<-ctx.Done():e=ctx.Err();attempt=r.cfg.MaximumRetries;case<-time.After(retryDelay(c.ID,attempt,r.cfg.InitialRetryDelay,r.cfg.MaximumRetryDelay)):}}};verified,ve:=r.store.Verify(ctx,r.owner,s.Fence,c);completed:=r.now().UTC();rec.CompletedAt=&completed;if e!=nil||ve!=nil||!verified{rec.State="FAILED";rec.Outcome="NO_TRADE";reason:="STALE_LEADER";if errors.Is(e,context.DeadlineExceeded)||errors.Is(ctx.Err(),context.DeadlineExceeded){reason="CYCLE_TIMEOUT"};rec.Reasons=[]string{reason};observeRuntime("failed",reason,c.Pair,c.Timeframe)}else{rec.Outcome=result.Outcome;rec.ResultFingerprint=fingerprint(result);if result.Outcome=="PAPER_ACCEPTED"||result.Outcome=="IDEMPOTENT_REPLAY"{rec.State="COMPLETED";r.mu.Lock();r.status.LastSuccess[c.Pair+"/"+c.Timeframe]=completed;r.mu.Unlock();observeRuntime("completed","PAPER_ACCEPTED",c.Pair,c.Timeframe)}else{rec.State="NO_TRADE";rec.Reasons=result.Reasons;reason:="NO_TRADE";if len(result.Reasons)>0{reason=result.Reasons[0]};observeRuntime("completed",reason,c.Pair,c.Timeframe)}};if saveErr:=r.store.Save(ctx,rec);saveErr!=nil{r.setState("degraded",true,"STORAGE_UNAVAILABLE")};runtimeCycleDuration.WithLabelValues(c.Pair,c.Timeframe,rec.Outcome).Observe(time.Since(startedWall).Seconds())}

func (r *paperRuntime) statusHandler(w http.ResponseWriter,q *http.Request){if q.Method!=http.MethodGet{jsonResponse(w,http.StatusMethodNotAllowed,map[string]any{"error":"method_not_allowed"});return};state:=r.Status();code:=http.StatusOK;if state.Enabled&&!state.Ready{code=http.StatusServiceUnavailable};jsonResponse(w,code,map[string]any{"runtime":state})}
func (r *paperRuntime) cyclesHandler(w http.ResponseWriter,q *http.Request){if q.Method!=http.MethodGet{jsonResponse(w,http.StatusMethodNotAllowed,map[string]any{"error":"method_not_allowed"});return};items,e:=r.store.Recent(q.Context(),50);if e!=nil{jsonResponse(w,http.StatusServiceUnavailable,map[string]any{"error":"STORAGE_UNAVAILABLE"});return};jsonResponse(w,http.StatusOK,map[string]any{"cycles":items,"warning":runtimeWarning})}

func envRuntimeConfig()(runtimeConfig,error){return parseRuntimeConfig(os.Getenv)}
func envRuntimeProvider(cfg runtimeConfig)(runtimeRequestProvider,error){endpoint:=strings.TrimSpace(os.Getenv("PAPER_AUTOMATION_REQUEST_URL"));if !cfg.Enabled{return nil,nil};if endpoint==""{return nil,errors.New("PAPER_AUTOMATION_REQUEST_URL is required when enabled")};u,e:=url.Parse(endpoint);if e!=nil||u.Scheme!="http"&&u.Scheme!="https"||u.Host==""{return nil,errors.New("invalid PAPER_AUTOMATION_REQUEST_URL")};return httpRuntimeRequestProvider{endpoint:endpoint,client:&http.Client{Timeout:cfg.CycleTimeout}},nil}
