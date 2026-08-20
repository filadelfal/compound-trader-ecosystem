package main

import (
	"context"
	"errors"
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
	records map[string]cycleRecord
	fail bool
}
func newMemoryRuntimeStore()*memoryRuntimeStore{return &memoryRuntimeStore{locks:map[string]string{},records:map[string]cycleRecord{}}}
func(s *memoryRuntimeStore)AcquireLeader(_ context.Context,o string,_ time.Duration)(uint64,bool,error){s.mu.Lock();defer s.mu.Unlock();if s.fail{return 0,false,errors.New("unavailable")};if s.leader!=""{return 0,false,nil};s.fence++;s.leader=o;return s.fence,true,nil}
func(s *memoryRuntimeStore)RenewLeader(_ context.Context,o string,f uint64,_ time.Duration)(bool,error){s.mu.Lock();defer s.mu.Unlock();if s.fail{return false,errors.New("unavailable")};return s.leader==o&&s.fence==f,nil}
func(s *memoryRuntimeStore)AcquireCycle(_ context.Context,c runtimeCycle,o string,f uint64,_ time.Duration)(bool,error){s.mu.Lock();defer s.mu.Unlock();if s.fail{return false,errors.New("unavailable")};if _,ok:=s.locks[c.ID];ok{return false,nil};s.locks[c.ID]=o+":"+string(rune(f));return true,nil}
func(s *memoryRuntimeStore)Verify(_ context.Context,o string,f uint64,c runtimeCycle)(bool,error){s.mu.Lock();defer s.mu.Unlock();if s.fail{return false,errors.New("unavailable")};_,locked:=s.locks[c.ID];return locked&&s.leader==o&&s.fence==f,nil}
func(s *memoryRuntimeStore)Load(_ context.Context,id string)(cycleRecord,error){s.mu.Lock();defer s.mu.Unlock();r,ok:=s.records[id];if !ok{return r,errors.New("missing")};return r,nil}
func(s *memoryRuntimeStore)Save(_ context.Context,r cycleRecord)error{s.mu.Lock();defer s.mu.Unlock();if s.fail{return errors.New("unavailable")};s.records[r.Cycle.ID]=r;return nil}
func(s *memoryRuntimeStore)Recent(_ context.Context,n int)([]cycleRecord,error){s.mu.Lock();defer s.mu.Unlock();out:=[]cycleRecord{};for _,r:=range s.records{out=append(out,r);if len(out)==n{break}};return out,nil}
func(s *memoryRuntimeStore)Healthy(context.Context)error{if s.fail{return errors.New("unavailable")};return nil}

type fixedRuntimeProvider struct{ request paperAutomationRequest; err error }
func(p fixedRuntimeProvider)Request(_ context.Context,c runtimeCycle)(paperAutomationRequest,error){v:=p.request;v.RequestID=c.ID;v.StrategyManager.RequestID=c.ID;v.StrategyManager.Pair=c.Pair;v.StrategyManager.Setups[0].Pair=c.Pair;v.StrategyManager.Setups[0].Timeframe=c.Timeframe;v.StrategyManager.Setups[0].Fingerprint=setupFingerprint(v.StrategyManager.Setups[0]);v.Readiness.Decision=validAutomationDecision(v.StrategyManager.Setups[0]);return v,p.err}

func TestRuntimeDisabledAndConfigurationFailClosed(t *testing.T){
	c,e:=parseRuntimeConfig(func(string)string{return ""});if e!=nil||c.Enabled{t.Fatalf("default must be disabled: %+v %v",c,e)}
	_,e=parseRuntimeConfig(func(k string)string{if k=="PAPER_AUTOMATION_MODE"{return "LIVE"};return ""});if e==nil{t.Fatal("non-PAPER mode accepted")}
	_,e=parseRuntimeConfig(func(k string)string{if k=="PAPER_AUTOMATION_PAIRS"{return "EURUSD,AUDUSD"};return ""});if e==nil{t.Fatal("unsupported pair accepted")}
}

func TestRuntimeUTCBoundariesSettlementAndIdentity(t *testing.T){
	now:=time.Date(2026,8,20,12,0,10,0,time.FixedZone("other",3600))
	if got:=latestClosedBoundary(now,time.Hour,15*time.Second);!got.Equal(time.Date(2026,8,20,10,0,0,0,time.UTC)){t.Fatalf("open candle selected: %s",got)}
	now=time.Date(2026,8,20,12,0,20,0,time.UTC);if got:=latestClosedBoundary(now,4*time.Hour,15*time.Second);!got.Equal(time.Date(2026,8,20,12,0,0,0,time.UTC)){t.Fatalf("H4 boundary wrong: %s",got)}
	c:=defaultRuntimeConfig();a:=newRuntimeCycle("EURUSD","H1",now,"v1",c.fingerprint());b:=newRuntimeCycle("EURUSD","H1",now,"v1",c.fingerprint());if !reflect.DeepEqual(a,b)||a.ID==""{t.Fatal("cycle identity is not deterministic")}
	c.WorkerLimit++;if a.ID==newRuntimeCycle("EURUSD","H1",now,"v1",c.fingerprint()).ID{t.Fatal("changed configuration reused identity")}
}

func TestRuntimeTwoReplicasAndStaleLeader(t *testing.T){
	store:=newMemoryRuntimeStore();f1,ok,e:=store.AcquireLeader(context.Background(),"one",time.Second);if e!=nil||!ok{t.Fatal(e)}
	if _,ok,_:=store.AcquireLeader(context.Background(),"two",time.Second);ok{t.Fatal("second leader acquired")}
	cycle:=newRuntimeCycle("EURUSD","H1",automationNow,"v1",defaultRuntimeConfig().fingerprint());if ok,_:=store.AcquireCycle(context.Background(),cycle,"one",f1,time.Second);!ok{t.Fatal("cycle lock failed")}
	store.mu.Lock();store.leader="two";store.fence++;store.mu.Unlock();if ok,_:=store.Verify(context.Background(),"one",f1,cycle);ok{t.Fatal("stale leader verified")}
}

func TestRuntimeEndToEndUsesMilestoneDAndIsIdempotent(t *testing.T){
	app,_:=automationApp();cfg:=defaultRuntimeConfig();cfg.Enabled=true;store:=newMemoryRuntimeStore();runtime,e:=newPaperRuntime(cfg,app,store,fixedRuntimeProvider{request:validAutomationRequest("placeholder")});if e!=nil{t.Fatal(e)}
	f,ok,_:=store.AcquireLeader(context.Background(),runtime.owner,time.Minute);if !ok{t.Fatal("leader")};runtime.mu.Lock();runtime.status.Leader=true;runtime.status.Fence=f;runtime.status.State="leader";runtime.mu.Unlock()
	cycle:=newRuntimeCycle("EURUSD","H1",automationNow,"strategy-set-v1",cfg.fingerprint());runtime.runCycle(context.Background(),cycle);record,e:=store.Load(context.Background(),cycle.ID);if e!=nil||record.State!="COMPLETED"||record.Outcome!="PAPER_ACCEPTED"{t.Fatalf("unexpected cycle: %+v %v",record,e)}
	runtime.runCycle(context.Background(),cycle);if len(store.records)!=1{t.Fatal("completed cycle replayed")}
}

func TestRuntimeStatusEndpointsAreReadOnly(t *testing.T){
	app,_:=automationApp();runtime,_:=newPaperRuntime(defaultRuntimeConfig(),app,newMemoryRuntimeStore(),nil)
	response:=httptest.NewRecorder();runtime.statusHandler(response,httptest.NewRequest(http.MethodGet,"/runtime",nil));if response.Code!=http.StatusOK||response.Header().Get("Content-Type")!="application/json"{t.Fatalf("status failed: %d",response.Code)}
	response=httptest.NewRecorder();runtime.statusHandler(response,httptest.NewRequest(http.MethodPost,"/runtime",nil));if response.Code!=http.StatusMethodNotAllowed{t.Fatal("status endpoint mutated")}
}

func TestRuntimeHasNoLiveExecutionSurface(t *testing.T){
	typ:=reflect.TypeOf(paperRuntime{});for i:=0;i<typ.NumField();i++{name:=typ.Field(i).Name;if name=="broker"||name=="liveOrder"||name=="liveExecutor"{t.Fatalf("live field exists: %s",name)}}
}
