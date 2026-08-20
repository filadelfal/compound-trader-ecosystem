package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

type memoryRuntimeStore struct {
	mu sync.Mutex
	leader string
	fence uint64
	locks map[string]string
	pairs map[string]string
	records map[string]cycleRecord
	fail bool
	clock func() time.Time
}
func newMemoryRuntimeStore()*memoryRuntimeStore{return &memoryRuntimeStore{locks:map[string]string{},pairs:map[string]string{},records:map[string]cycleRecord{},clock:func()time.Time{return automationNow}}}
func(s *memoryRuntimeStore)AcquireLeader(_ context.Context,o string,_ time.Duration)(uint64,bool,error){s.mu.Lock();defer s.mu.Unlock();if s.fail{return 0,false,errors.New("unavailable")};if s.leader!=""{return 0,false,nil};s.fence++;s.leader=o;return s.fence,true,nil}
func(s *memoryRuntimeStore)RenewLeader(_ context.Context,o string,f uint64,_ time.Duration)(bool,error){s.mu.Lock();defer s.mu.Unlock();if s.fail{return false,errors.New("unavailable")};return s.leader==o&&s.fence==f,nil}
func(s *memoryRuntimeStore)AcquireCycle(_ context.Context,c runtimeCycle,o string,f uint64,_ time.Duration)(bool,error){s.mu.Lock();defer s.mu.Unlock();if s.fail{return false,errors.New("unavailable")};if _,ok:=s.locks[c.ID];ok{return false,nil};s.locks[c.ID]=fmt.Sprintf("%s:%d",o,f);return true,nil}
func(s *memoryRuntimeStore)AcquirePair(_ context.Context,c runtimeCycle,o string,f uint64,_ time.Duration)(bool,error){s.mu.Lock();defer s.mu.Unlock();if s.fail{return false,errors.New("unavailable")};if _,ok:=s.pairs[c.Pair];ok{return false,nil};s.pairs[c.Pair]=fmt.Sprintf("%s:%d",o,f);return true,nil}
func(s *memoryRuntimeStore)Verify(_ context.Context,o string,f uint64,c runtimeCycle)(bool,error){s.mu.Lock();defer s.mu.Unlock();if s.fail{return false,errors.New("unavailable")};expected:=fmt.Sprintf("%s:%d",o,f);return s.locks[c.ID]==expected&&s.pairs[c.Pair]==expected&&s.leader==o&&s.fence==f,nil}
func(s *memoryRuntimeStore)Release(_ context.Context,c runtimeCycle,o string,f uint64)error{s.mu.Lock();defer s.mu.Unlock();expected:=fmt.Sprintf("%s:%d",o,f);if s.locks[c.ID]==expected{delete(s.locks,c.ID)};if s.pairs[c.Pair]==expected{delete(s.pairs,c.Pair)};return nil}
func(s *memoryRuntimeStore)Load(_ context.Context,id string)(cycleRecord,error){s.mu.Lock();defer s.mu.Unlock();r,ok:=s.records[id];if !ok{return r,errors.New("missing")};return r,nil}
func(s *memoryRuntimeStore)Save(_ context.Context,r cycleRecord)error{s.mu.Lock();defer s.mu.Unlock();if s.fail{return errors.New("unavailable")};s.records[r.Cycle.ID]=r;return nil}
func(s *memoryRuntimeStore)Recent(_ context.Context,n int)([]cycleRecord,error){s.mu.Lock();defer s.mu.Unlock();out:=[]cycleRecord{};for _,r:=range s.records{out=append(out,r);if len(out)==n{break}};return out,nil}
func(s *memoryRuntimeStore)Healthy(context.Context)error{if s.fail{return errors.New("unavailable")};return nil}
func(s *memoryRuntimeStore)ServerTime(context.Context)(time.Time,error){if s.fail{return time.Time{},errors.New("unavailable")};return s.clock(),nil}

type fixedRuntimeProvider struct{ request paperAutomationRequest; now func() time.Time; staleQuote bool; err error }
func(p fixedRuntimeProvider)Request(_ context.Context,c runtimeCycle)(paperAutomationRequest,error){
	v:=p.request
	now:=p.now().UTC()
	v.RequestID=c.ID
	v.StrategyManager.RequestID=c.ID
	v.StrategyManager.Pair=c.Pair
	v.StrategyManager.Setups[0].Pair=c.Pair
	v.StrategyManager.Setups[0].Timeframe=c.Timeframe
	v.StrategyManager.Setups[0].Fingerprint=setupFingerprint(v.StrategyManager.Setups[0])
	v.Readiness.Decision=validAutomationDecision(v.StrategyManager.Setups[0])
	v.Readiness.DecidedAt=now.Add(-time.Hour).Format(time.RFC3339Nano)
	v.Quote.Timestamp=now.Add(-time.Second).Format(time.RFC3339Nano)
	if p.staleQuote { v.Quote.Timestamp=now.Add(-time.Duration(v.Freshness.QuoteMaxAgeSeconds+1)*time.Second).Format(time.RFC3339Nano) }
	v.Candles.H1=[]automationCandle{automationCandleAt(now.Add(-2*time.Hour)),automationCandleAt(now.Add(-time.Hour))}
	v.Candles.H4=[]automationCandle{automationCandleAt(now.Add(-8*time.Hour)),automationCandleAt(now.Add(-4*time.Hour))}
	return v,p.err
}
type runtimeProviderFunc func(context.Context,runtimeCycle)(paperAutomationRequest,error)
func(f runtimeProviderFunc)Request(ctx context.Context,c runtimeCycle)(paperAutomationRequest,error){return f(ctx,c)}

func TestRuntimeDisabledAndConfigurationFailClosed(t *testing.T){
	c,e:=parseRuntimeConfig(func(string)string{return ""});if e!=nil||c.Enabled{t.Fatalf("default must be disabled: %+v %v",c,e)}
	_,e=parseRuntimeConfig(func(k string)string{if k=="PAPER_AUTOMATION_MODE"{return "LIVE"};return ""});if e==nil{t.Fatal("non-PAPER mode accepted")}
	_,e=parseRuntimeConfig(func(k string)string{if k=="PAPER_AUTOMATION_PAIRS"{return "EURUSD,AUDUSD"};return ""});if e==nil{t.Fatal("unsupported pair accepted")}
}

func TestRuntimeUTCBoundariesSettlementAndIdentity(t *testing.T){
	now:=time.Date(2026,8,20,12,0,10,0,time.FixedZone("other",3600))
	if got:=latestClosedBoundary(now,time.Hour,15*time.Second);!got.Equal(time.Date(2026,8,20,10,0,0,0,time.UTC)){t.Fatalf("open candle selected: %s",got)}
	now=time.Date(2026,8,20,12,0,20,0,time.UTC);if got:=latestClosedBoundary(now,4*time.Hour,15*time.Second);!got.Equal(time.Date(2026,8,20,12,0,0,0,time.UTC)){t.Fatalf("H4 boundary wrong: %s",got)}
	c:=defaultRuntimeConfig();a:=newRuntimeCycle("EURUSD","H1",now,"v1",c.fingerprint());b:=newRuntimeCycle("EURUSD","H1",now,"v1",c.fingerprint());if !reflect.DeepEqual(a,b)||a.ID==""{t.Fatal("cycle identity is not deterministic")};if !validPaperIdentifier(a.ID){t.Fatalf("cycle ID violates Milestone D identifier contract: %q",a.ID)}
	c.WorkerLimit++;if a.ID==newRuntimeCycle("EURUSD","H1",now,"v1",c.fingerprint()).ID{t.Fatal("changed configuration reused identity")}
}

func TestRuntimeFixtureMatchesMilestoneDMarketContract(t *testing.T){
	app,_:=automationApp();cfg:=defaultRuntimeConfig();cfg.Enabled=true;cycle:=newRuntimeCycle("EURUSD","H1",automationNow,"strategy-set-v1",cfg.fingerprint());provider:=fixedRuntimeProvider{request:validAutomationRequest("placeholder"),now:app.currentTime};request,e:=provider.Request(context.Background(),cycle);if e!=nil{t.Fatal(e)}
	if !validPaperIdentifier(request.RequestID){t.Fatalf("invalid request ID: %q",request.RequestID)}
	if request.Mode!="PAPER"||request.StrategyManager.RequestID!=request.RequestID||request.StrategyManager.Pair!="EURUSD"||len(request.StrategyManager.Setups)!=1{t.Fatalf("strategy manager identity mismatch: %+v",request.StrategyManager)}
	setup:=request.StrategyManager.Setups[0];if setup.Fingerprint!=setupFingerprint(setup){t.Fatalf("setup fingerprint mismatch: %s",setup.Fingerprint)}
	if !validActiveReadinessDecision(request.Readiness.Decision){t.Fatalf("invalid readiness decision: %+v",request.Readiness.Decision)}
	quoteAt,e:=time.Parse(time.RFC3339Nano,request.Quote.Timestamp);if e!=nil||request.Quote.Bid<=0||request.Quote.Ask<=request.Quote.Bid||automationNow.Sub(quoteAt)>time.Duration(request.Freshness.QuoteMaxAgeSeconds)*time.Second{t.Fatalf("invalid quote contract: %+v",request.Quote)}
	maxAge:=time.Duration(request.Freshness.CandleMaxAgeSeconds)*time.Second;if !validateAutomationCandles(request.Candles.H1,time.Hour,automationNow,maxAge){t.Fatalf("invalid H1 fixture: %+v",request.Candles.H1)};if !validateAutomationCandles(request.Candles.H4,4*time.Hour,automationNow,maxAge){t.Fatalf("invalid H4 fixture: %+v",request.Candles.H4)}
	result,status:=app.evaluatePaperAutomation(context.Background(),request);if status!=http.StatusAccepted||result.Outcome!="PAPER_ACCEPTED"{t.Fatalf("Milestone D rejected traced fixture: status=%d result=%+v",status,result)}
}

func TestRuntimeTwoReplicasAndStaleLeader(t *testing.T){
	store:=newMemoryRuntimeStore();f1,ok,e:=store.AcquireLeader(context.Background(),"one",time.Second);if e!=nil||!ok{t.Fatal(e)}
	if _,ok,_:=store.AcquireLeader(context.Background(),"two",time.Second);ok{t.Fatal("second leader acquired")}
	cycle:=newRuntimeCycle("EURUSD","H1",automationNow,"v1",defaultRuntimeConfig().fingerprint());if ok,_:=store.AcquireCycle(context.Background(),cycle,"one",f1,time.Second);!ok{t.Fatal("cycle lock failed")};if ok,_:=store.AcquirePair(context.Background(),cycle,"one",f1,time.Second);!ok{t.Fatal("pair lock failed")}
	store.mu.Lock();store.leader="two";store.fence++;store.mu.Unlock();if ok,_:=store.Verify(context.Background(),"one",f1,cycle);ok{t.Fatal("stale leader verified")}
}

func TestRuntimeRecoveryIsChronologicalAndBounded(t *testing.T){
	app,_:=automationApp();cfg:=defaultRuntimeConfig();cfg.Enabled=true;cfg.RecoveryWindow=4*time.Hour;cfg.QueueCapacity=32;runtime,_:=newPaperRuntime(cfg,app,newMemoryRuntimeStore(),fixedRuntimeProvider{request:validAutomationRequest("placeholder"),now:app.currentTime});cycles:=runtime.recoveryCycles(automationNow);if len(cycles)==0{t.Fatal("no recovery cycles")};for i,c:=range cycles{if automationNow.Sub(c.CandleClose)>cfg.RecoveryWindow{t.Fatalf("expired cycle recovered: %+v",c)};if i>0&&c.CandleClose.Before(cycles[i-1].CandleClose){t.Fatalf("recovery is not chronological: %s before %s",c.CandleClose,cycles[i-1].CandleClose)}}
}

func TestRuntimeSamePairLockAndOwnerSafeRelease(t *testing.T){
	store:=newMemoryRuntimeStore();cycle:=newRuntimeCycle("EURUSD","H1",automationNow,"v1",defaultRuntimeConfig().fingerprint());f,_,_:=store.AcquireLeader(context.Background(),"one",time.Minute);ok,_:=store.AcquirePair(context.Background(),cycle,"one",f,time.Minute);if !ok{t.Fatal("first pair lock failed")};other:=newRuntimeCycle("EURUSD","H4",automationNow,"v1",defaultRuntimeConfig().fingerprint());if ok,_:=store.AcquirePair(context.Background(),other,"two",f+1,time.Minute);ok{t.Fatal("same pair lock duplicated")};_ = store.Release(context.Background(),cycle,"two",f+1);if store.pairs[cycle.Pair]==""{t.Fatal("non-owner released pair lock")};_ = store.Release(context.Background(),cycle,"one",f);if store.pairs[cycle.Pair]!=""{t.Fatal("owner did not release pair lock")}
}

func TestRuntimeStaleLeaderRejectedBeforePaperOrder(t *testing.T){
	app,_:=automationApp();cfg:=defaultRuntimeConfig();cfg.Enabled=true;store:=newMemoryRuntimeStore();base:=fixedRuntimeProvider{request:validAutomationRequest("placeholder"),now:app.currentTime};provider:=runtimeProviderFunc(func(ctx context.Context,c runtimeCycle)(paperAutomationRequest,error){v,e:=base.Request(ctx,c);store.mu.Lock();store.leader="replacement";store.fence++;store.mu.Unlock();return v,e});runtime,_:=newPaperRuntime(cfg,app,store,provider);f,_,_:=store.AcquireLeader(context.Background(),runtime.owner,time.Minute);runtime.mu.Lock();runtime.status.Leader=true;runtime.status.Fence=f;runtime.status.State="leader";runtime.mu.Unlock();cycle:=newRuntimeCycle("EURUSD","H1",automationNow,"v1",cfg.fingerprint());runtime.runCycle(context.Background(),cycle);record,_:=store.Load(context.Background(),cycle.ID);if len(record.Reasons)!=1||record.Reasons[0]!="STALE_LEADER"{t.Fatalf("stale leader did not fail closed: %+v",record)};orders:=app.paperStore.(*memoryPaperOrderStore);if len(orders.orders)!=0{t.Fatalf("stale leader created paper order: %+v",orders.orders)}
}

func TestRuntimeOutageClockSkewBackpressureAndShutdown(t *testing.T){
	app,_:=automationApp();cfg:=defaultRuntimeConfig();cfg.Enabled=true;cfg.RecoveryWindow=time.Hour;cfg.QueueCapacity=8;cfg.WorkerLimit=1;cfg.LeaderRenewalInterval=5*time.Millisecond;cfg.LeaderLeaseDuration=20*time.Millisecond;provider:=fixedRuntimeProvider{request:validAutomationRequest("placeholder"),now:app.currentTime};store:=newMemoryRuntimeStore();store.clock=func()time.Time{return automationNow.Add(time.Minute)};runtime,_:=newPaperRuntime(cfg,app,store,provider);runtime.Start(context.Background());time.Sleep(15*time.Millisecond);if state:=runtime.Status();state.State!="degraded"||len(state.Reasons)==0||state.Reasons[0]!="CLOCK_SKEW"{t.Fatalf("clock skew not degraded: %+v",state)};shutdown,cancel:=context.WithTimeout(context.Background(),time.Second);defer cancel();if e:=runtime.Stop(shutdown);e!=nil{t.Fatal(e)};if runtime.Status().State!="paused"{t.Fatalf("shutdown did not pause: %+v",runtime.Status())}

	store=newMemoryRuntimeStore();store.fail=true;runtime,_=newPaperRuntime(cfg,app,store,provider);runtime.Start(context.Background());time.Sleep(15*time.Millisecond);if state:=runtime.Status();state.State!="degraded"{t.Fatalf("storage outage reported healthy: %+v",state)};_ = runtime.Stop(shutdown)
}

func TestRuntimeBoundedRetryTimeoutAndQueueBackpressure(t *testing.T){
	app,_:=automationApp();cfg:=defaultRuntimeConfig();cfg.Enabled=true;cfg.MaximumRetries=2;cfg.InitialRetryDelay=time.Millisecond;cfg.MaximumRetryDelay=2*time.Millisecond;store:=newMemoryRuntimeStore();base:=fixedRuntimeProvider{request:validAutomationRequest("placeholder"),now:app.currentTime};attempts:=0;provider:=runtimeProviderFunc(func(ctx context.Context,c runtimeCycle)(paperAutomationRequest,error){attempts++;if attempts<3{return paperAutomationRequest{},errors.New("temporary outage")};return base.Request(ctx,c)});runtime,_:=newPaperRuntime(cfg,app,store,provider);f,_,_:=store.AcquireLeader(context.Background(),runtime.owner,time.Minute);runtime.mu.Lock();runtime.status.Leader=true;runtime.status.Fence=f;runtime.status.State="leader";runtime.mu.Unlock();cycle:=newRuntimeCycle("EURUSD","H1",automationNow,"v1",cfg.fingerprint());runtime.runCycle(context.Background(),cycle);record,_:=store.Load(context.Background(),cycle.ID);if record.State!="COMPLETED"||record.Attempts!=3{t.Fatalf("bounded retry failed: %+v attempts=%d",record,attempts)}

	cfg.QueueCapacity=cfg.WorkerLimit;runtime,_=newPaperRuntime(cfg,app,newMemoryRuntimeStore(),base);runtime.enqueue(newRuntimeCycle("EURUSD","H1",automationNow,"v1",cfg.fingerprint()));runtime.enqueue(newRuntimeCycle("GBPUSD","H1",automationNow,"v1",cfg.fingerprint()));runtime.enqueue(newRuntimeCycle("USDJPY","H1",automationNow,"v1",cfg.fingerprint()));if state:=runtime.Status();state.State!="degraded"||len(state.Reasons)==0||state.Reasons[0]!="QUEUE_FULL"{t.Fatalf("queue backpressure not explicit: %+v",state)}

	cfg.CycleTimeout=5*time.Millisecond;store=newMemoryRuntimeStore();blocking:=runtimeProviderFunc(func(ctx context.Context,c runtimeCycle)(paperAutomationRequest,error){<-ctx.Done();return paperAutomationRequest{},ctx.Err()});runtime,_=newPaperRuntime(cfg,app,store,blocking);f,_,_=store.AcquireLeader(context.Background(),runtime.owner,time.Minute);runtime.mu.Lock();runtime.status.Leader=true;runtime.status.Fence=f;runtime.status.State="leader";runtime.mu.Unlock();cycle=newRuntimeCycle("GBPUSD","H1",automationNow,"v1",cfg.fingerprint());runtime.runCycle(context.Background(),cycle);record,_=store.Load(context.Background(),cycle.ID);if len(record.Reasons)!=1||record.Reasons[0]!="CYCLE_TIMEOUT"{t.Fatalf("timeout did not fail closed: %+v",record)}
}

func TestRuntimeRestartSkipsCompletedCycle(t *testing.T){
	app,_:=automationApp();cfg:=defaultRuntimeConfig();cfg.Enabled=true;store:=newMemoryRuntimeStore();provider:=fixedRuntimeProvider{request:validAutomationRequest("placeholder"),now:app.currentTime};first,_:=newPaperRuntime(cfg,app,store,provider);f,_,_:=store.AcquireLeader(context.Background(),first.owner,time.Minute);first.mu.Lock();first.status.Leader=true;first.status.Fence=f;first.status.State="leader";first.mu.Unlock();cycle:=newRuntimeCycle("EURUSD","H1",automationNow,"v1",cfg.fingerprint());first.runCycle(context.Background(),cycle);store.mu.Lock();store.leader="";store.mu.Unlock();second,_:=newPaperRuntime(cfg,app,store,provider);f,_,_=store.AcquireLeader(context.Background(),second.owner,time.Minute);second.mu.Lock();second.status.Leader=true;second.status.Fence=f;second.status.State="leader";second.mu.Unlock();second.runCycle(context.Background(),cycle);orders:=app.paperStore.(*memoryPaperOrderStore);if len(orders.orders)!=1{t.Fatalf("restart duplicated paper order: %+v",orders.orders)}
}

func TestRuntimeEndToEndUsesMilestoneDAndIsIdempotent(t *testing.T){
	app,_:=automationApp();cfg:=defaultRuntimeConfig();cfg.Enabled=true;store:=newMemoryRuntimeStore();runtime,e:=newPaperRuntime(cfg,app,store,fixedRuntimeProvider{request:validAutomationRequest("placeholder"),now:app.currentTime});if e!=nil{t.Fatal(e)}
	if !runtime.now().Equal(automationNow) { t.Fatalf("runtime clock is not coordinator clock: %s",runtime.now()) }
	f,ok,_:=store.AcquireLeader(context.Background(),runtime.owner,time.Minute);if !ok{t.Fatal("leader")};runtime.mu.Lock();runtime.status.Leader=true;runtime.status.Fence=f;runtime.status.State="leader";runtime.mu.Unlock()
	cycle:=newRuntimeCycle("EURUSD","H1",automationNow,"strategy-set-v1",cfg.fingerprint());runtime.runCycle(context.Background(),cycle);record,e:=store.Load(context.Background(),cycle.ID);if e!=nil||record.State!="COMPLETED"||record.Outcome!="PAPER_ACCEPTED"{t.Fatalf("unexpected cycle: %+v %v",record,e)}
	runtime.runCycle(context.Background(),cycle);if len(store.records)!=1{t.Fatal("completed cycle replayed")}
}

func TestRuntimeFixedClockRejectsStaleMarketData(t *testing.T){
	app,_:=automationApp();cfg:=defaultRuntimeConfig();cfg.Enabled=true;store:=newMemoryRuntimeStore();runtime,e:=newPaperRuntime(cfg,app,store,fixedRuntimeProvider{request:validAutomationRequest("placeholder"),now:app.currentTime,staleQuote:true});if e!=nil{t.Fatal(e)}
	f,ok,_:=store.AcquireLeader(context.Background(),runtime.owner,time.Minute);if !ok{t.Fatal("leader")};runtime.mu.Lock();runtime.status.Leader=true;runtime.status.Fence=f;runtime.status.State="leader";runtime.mu.Unlock()
	cycle:=newRuntimeCycle("EURUSD","H1",automationNow,"strategy-set-v1",cfg.fingerprint());runtime.runCycle(context.Background(),cycle);record,e:=store.Load(context.Background(),cycle.ID);if e!=nil||record.State!="NO_TRADE"||record.Outcome!="NO_TRADE"||len(record.Reasons)!=1||record.Reasons[0]!="STALE_QUOTE"{t.Fatalf("stale market data did not fail closed: %+v %v",record,e)}
}

func TestRuntimeStatusEndpointsAreReadOnly(t *testing.T){
	app,_:=automationApp();runtime,_:=newPaperRuntime(defaultRuntimeConfig(),app,newMemoryRuntimeStore(),nil)
	response:=httptest.NewRecorder();runtime.statusHandler(response,httptest.NewRequest(http.MethodGet,"/runtime",nil));if response.Code!=http.StatusOK||response.Header().Get("Content-Type")!="application/json"{t.Fatalf("status failed: %d",response.Code)}
	response=httptest.NewRecorder();runtime.statusHandler(response,httptest.NewRequest(http.MethodPost,"/runtime",nil));if response.Code!=http.StatusMethodNotAllowed{t.Fatal("status endpoint mutated")}
}

func TestRuntimeHasNoLiveExecutionSurface(t *testing.T){
	typ:=reflect.TypeOf(paperRuntime{});for i:=0;i<typ.NumField();i++{name:=typ.Field(i).Name;if name=="broker"||name=="liveOrder"||name=="liveExecutor"{t.Fatalf("live field exists: %s",name)}}
}
