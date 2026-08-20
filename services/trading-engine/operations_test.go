package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

var operationsNow=time.Date(2026,8,21,0,0,0,0,time.UTC)
func operationsPosition(id,pair,status string,pnl float64)paperPosition{p:=paperPosition{OrderID:id,RequestID:"request-"+id,Pair:pair,Strategy:"EMA_PULLBACK",Direction:"BUY",Lots:.1,Entry:1.1,StopLoss:1.095,TakeProfit:1.11,PipValuePerStandardLot:10,Status:status,CurrentPrice:1.1,RealizedPnL:pnl,OpenedAt:operationsNow.Add(-time.Hour)};if status=="CLOSED"{v:=operationsNow.Add(-time.Minute);p.ClosedAt=&v;p.CloseReason="TAKE_PROFIT";p.ExitPrice=1.11};return p}
func operationsOrder(id,pair string)paperOrder{return paperOrder{OrderID:id,RequestID:"request-"+id,Mode:"PAPER",Pair:pair,Strategy:"EMA_PULLBACK",Direction:"BUY",Lots:.1,Entry:1.1,StopLoss:1.095,TakeProfit:1.11,Status:"PAPER_ACCEPTED",CreatedAt:operationsNow.Add(-time.Hour)}}
func consistentEvidence()operationsEvidence{p:=operationsPosition("paper-one","EURUSD","OPEN",0);event:=paperJournalEvent{EventID:"open-one",Type:"POSITION_OPENED",RecordedAt:p.OpenedAt,Position:p};return operationsEvidence{Orders:[]paperOrder{operationsOrder(p.OrderID,p.Pair)},Positions:[]paperPosition{p},Journals:map[string][]paperJournalEvent{p.OrderID:{event}},Risk:operationsRiskState{StartingEquity:10000,CurrentEquity:10000,PeakEquity:10000,OpenPositionCount:1,PairExposure:map[string]bool{"EURUSD":true},TradingEnabled:true}}}
func reasons(fs []reconciliationFinding)map[string]bool{out:=map[string]bool{};for _,f:=range fs{out[f.Reason]=true};return out}

func TestOperationsFullyReconciledState(t *testing.T){if fs:=reconcileEvidence("run-ok",consistentEvidence(),operationsNow);len(fs)!=0{t.Fatalf("consistent state rejected: %+v",fs)}}

func TestOperationsOrderPositionAndExposureFindings(t *testing.T){tests:=[]struct{name,want string;mutate func(*operationsEvidence)}{
	{"order without position","ORDER_WITHOUT_POSITION",func(e *operationsEvidence){e.Positions=nil;e.Journals=map[string][]paperJournalEvent{};e.Risk.OpenPositionCount=0;e.Risk.PairExposure=map[string]bool{}}},
	{"position without order","POSITION_WITHOUT_ORDER",func(e *operationsEvidence){e.Orders=nil}},
	{"duplicate position","DUPLICATE_POSITION",func(e *operationsEvidence){e.Positions=append(e.Positions,e.Positions[0])}},
	{"missing exposure","MISSING_PAIR_EXPOSURE",func(e *operationsEvidence){e.Risk.PairExposure=map[string]bool{}}},
	{"orphan exposure","ORPHAN_PAIR_EXPOSURE",func(e *operationsEvidence){e.Risk.PairExposure["GBPUSD"]=true}},
	{"count mismatch","OPEN_POSITION_COUNT_MISMATCH",func(e *operationsEvidence){e.Risk.OpenPositionCount=0}},
};for _,test:=range tests{t.Run(test.name,func(t *testing.T){e:=consistentEvidence();test.mutate(&e);if !reasons(reconcileEvidence("run-"+test.want,e,operationsNow))[test.want]{t.Fatalf("missing %s",test.want)}})}}

func TestOperationsLifecycleAndRiskFindings(t *testing.T){e:=consistentEvidence();p:=operationsPosition("paper-closed","GBPUSD","CLOSED",-100);e.Orders=append(e.Orders,operationsOrder(p.OrderID,p.Pair));e.Positions=append(e.Positions,p);e.Journals[p.OrderID]=[]paperJournalEvent{{EventID:"close-a",Type:"POSITION_CLOSED_STOP_LOSS",RecordedAt:operationsNow.Add(-2*time.Minute),Position:p},{EventID:"close-b",Type:"POSITION_CLOSED_TAKE_PROFIT",RecordedAt:operationsNow.Add(-time.Minute),Position:p}};e.Risk.DailyRealizedPnL=0;e.Risk.DailyLossPercent=0;e.Risk.DrawdownPercent=0;got:=reasons(reconcileEvidence("run-risk",e,operationsNow));for _,want:=range []string{"CONFLICTING_CLOSURE_EVENTS","REALIZED_PNL_MISMATCH","DAILY_LOSS_MISMATCH"}{if !got[want]{t.Fatalf("missing %s: %+v",want,got)}}
	e.Risk.CurrentEquity=9000;e.Risk.PeakEquity=10000;if !reasons(reconcileEvidence("run-dd",e,operationsNow))["DRAWDOWN_MISMATCH"]{t.Fatal("missing drawdown mismatch")}}

func TestOperationsLedgerAppendReplayConflictAndBrokenChain(t *testing.T){store:=newMemoryPaperOperationsStore();first:=makeLedgerEvent("GENESIS","event-one","request-one","","EURUSD","H1","EMA_PULLBACK","1.0.0","NO_TRADE",operationsNow,map[string]any{"outcome":"NO_TRADE"},"SYSTEM","",[]string{"NO_TRADE"});if _,created,e:=store.Append(context.Background(),first);e!=nil||!created{t.Fatalf("append failed: %v",e)};if _,created,e:=store.Append(context.Background(),first);e!=nil||created{t.Fatalf("replay failed: %v",e)};changed:=first;changed.ReasonCodes=[]string{"CHANGED"};changed.LedgerFingerprint=ledgerFingerprint(changed);if _,_,e:=store.Append(context.Background(),changed);e==nil{t.Fatal("changed reuse accepted")};evidence:=consistentEvidence();evidence.Ledger=[]paperLedgerEvent{first};evidence.Ledger[0].PreviousStateFingerprint="broken";if !reasons(reconcileEvidence("run-chain",evidence,operationsNow))["BROKEN_LEDGER_CHAIN"]{t.Fatal("broken chain not detected")}}

func TestOperationsCriticalFindingFailsClosedAndActivatesKillSwitch(t *testing.T){store:=newMemoryPaperOperationsStore();e:=consistentEvidence();e.Orders[0].Mode="LIVE";ops:=newPaperOperations(store,func()time.Time{return operationsNow},func(context.Context)(operationsEvidence,error){return e,nil});run,fs,err:=ops.reconcile(context.Background(),"startup");if err!=nil||!run.Blocking||len(fs)==0{t.Fatalf("reconcile failed: %+v %+v %v",run,fs,err)};state:=ops.status();if state["ready"].(bool)||!state["killSwitchActive"].(bool){t.Fatalf("critical finding did not fail closed: %+v",state)};if reason:=ops.acceptanceGate(context.Background(),paperAutomationRequest{});reason!="KILL_SWITCH_ACTIVE"{t.Fatalf("unexpected gate: %s",reason)}}

func TestOperationsDailyRollupReproducible(t *testing.T){e:=consistentEvidence();closed:=operationsPosition("paper-two","GBPUSD","CLOSED",100);e.Orders=append(e.Orders,operationsOrder(closed.OrderID,closed.Pair));e.Positions=append(e.Positions,closed);e.Risk.DailyRealizedPnL=100;left:=buildDailyRollup("2026-08-21",e);right:=buildDailyRollup("2026-08-21",e);if !reflect.DeepEqual(left,right)||left.Fingerprint==""||left.TradesOpened!=2||left.TradesClosed!=1||left.Wins!=1{t.Fatalf("bad rollup: %+v %+v",left,right)}}

func TestOperationsCommandsDefaultDenyAndNoLiveSurface(t *testing.T){ops:=newPaperOperations(newMemoryPaperOperationsStore(),func()time.Time{return operationsNow},nil);if err:=ops.executeOperatorCommand(context.Background(),operatorCommand{RequestID:"admin-one",Action:"PAUSE",Reason:"maintenance",ActorID:"operator"});err==nil{t.Fatal("operator command exposed without trusted auth")};if err:=validateNoLiveOperationsSurface();err!=nil{t.Fatal(err)}}

func TestOperationsEvidenceFailureFailsClosed(t *testing.T){ops:=newPaperOperations(newMemoryPaperOperationsStore(),func()time.Time{return operationsNow},func(context.Context)(operationsEvidence,error){return operationsEvidence{},errors.New("redis unavailable")});if _,_,err:=ops.reconcile(context.Background(),"startup");err==nil{t.Fatal("dependency failure accepted")};if reason:=ops.acceptanceGate(context.Background(),paperAutomationRequest{});reason!="RECONCILIATION_BLOCKING"{t.Fatalf("unexpected fail-closed reason: %s",reason)}}

func TestOperationsAcceptedFlowWritesVerifiableLedger(t *testing.T){store:=newMemoryPaperOperationsStore();ops:=newPaperOperations(store,func()time.Time{return operationsNow},nil);input:=paperAutomationRequest{RequestID:"cycle-eurusd-h1"};setup:=automationSetup{Pair:"EURUSD",Timeframe:"H1",Strategy:"EMA_PULLBACK",StrategyVersion:"1.0.0"};order:=operationsOrder("paper-ledger","EURUSD");position:=operationsPosition(order.OrderID,"EURUSD","OPEN",0);if err:=ops.recordAccepted(context.Background(),input,setup,order,position);err!=nil{t.Fatal(err)};events,err:=store.Ledger(context.Background(),0,10);if err!=nil||len(events)!=2||events[0].EventType!="PAPER_ORDER_ACCEPTED"||events[1].PreviousStateFingerprint!=events[0].LedgerFingerprint{t.Fatalf("invalid accepted ledger: %+v %v",events,err)}}

func TestOperationsEndpointsAreStrictlyReadOnly(t *testing.T){store:=newMemoryPaperOperationsStore();ops:=newPaperOperations(store,func()time.Time{return operationsNow},nil);handlers:=[]http.HandlerFunc{ops.statusHandler,ops.findingsHandler,ops.ledgerHandler,ops.rollupHandler};for _,handler:=range handlers{recorder:=httptest.NewRecorder();handler(recorder,httptest.NewRequest(http.MethodPost,"/",nil));if recorder.Code!=http.StatusMethodNotAllowed{t.Fatalf("mutation method exposed: %d",recorder.Code)}}}
