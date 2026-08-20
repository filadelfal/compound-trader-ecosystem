package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const operationsSchemaVersion = "paper-operations.v1"
const operationsWarning = "PAPER_ONLY_NO_LIVE_AUTHORITY"

var ledgerEventTypes = map[string]bool{"RUNTIME_CYCLE_EVALUATED":true,"NO_TRADE":true,"SETUP_APPROVED":true,"RISK_APPROVED":true,"PAPER_ORDER_ACCEPTED":true,"PAPER_POSITION_OPENED":true,"POSITION_MARKED":true,"POSITION_CLOSED_STOP_LOSS":true,"POSITION_CLOSED_TAKE_PROFIT":true,"ADMIN_PAUSED":true,"ADMIN_RESUMED":true,"KILL_SWITCH_ACTIVATED":true,"KILL_SWITCH_RELEASE_REQUESTED":true,"RECONCILIATION_FINDING":true,"RECONCILIATION_RESOLVED":true,"DERIVED_REPAIR_PREVIEWED":true,"DERIVED_REPAIR_APPLIED":true}
var findingSeverities = map[string]bool{"INFO":true,"WARNING":true,"BLOCKING":true,"CRITICAL":true}
var findingReasons = map[string]bool{"UNSUPPORTED_OR_LIVE_ORDER":true,"POSITION_WITHOUT_ORDER":true,"UNSUPPORTED_POSITION_IDENTITY":true,"INVALID_POSITION_STATUS":true,"MISSING_LIFECYCLE_EVENTS":true,"INVALID_TRANSITION_ORDER":true,"MISSING_CLOSURE_EVENT":true,"CONFLICTING_CLOSURE_EVENTS":true,"CLOSED_POSITION_COUNTED_OPEN":true,"ORDER_WITHOUT_POSITION":true,"DUPLICATE_POSITION":true,"ORPHAN_PAIR_EXPOSURE":true,"MISSING_PAIR_EXPOSURE":true,"OPEN_POSITION_COUNT_MISMATCH":true,"REALIZED_PNL_MISMATCH":true,"DAILY_LOSS_MISMATCH":true,"DRAWDOWN_MISMATCH":true,"BROKEN_LEDGER_CHAIN":true,"CONSISTENT":true,"INCONSISTENT":true,"OTHER":true}
var operationsEvents = prometheus.NewCounterVec(prometheus.CounterOpts{Name:"paper_operations_events_total",Help:"Bounded PAPER operations events."},[]string{"event","severity","reason"})
var operationsReady = prometheus.NewGauge(prometheus.GaugeOpts{Name:"paper_operations_ready",Help:"One when reconciliation permits PAPER evaluation."})

func init(){prometheus.MustRegister(operationsEvents,operationsReady)}

type paperLedgerEvent struct {
	EventID string `json:"eventId"`
	CorrelationID string `json:"correlationId"`
	CycleID string `json:"cycleId,omitempty"`
	Pair string `json:"pair,omitempty"`
	Timeframe string `json:"timeframe,omitempty"`
	Strategy string `json:"strategy,omitempty"`
	StrategyVersion string `json:"strategyVersion,omitempty"`
	EventType string `json:"eventType"`
	Timestamp time.Time `json:"timestamp"`
	PreviousStateFingerprint string `json:"previousStateFingerprint"`
	CurrentStateFingerprint string `json:"currentStateFingerprint"`
	PayloadFingerprint string `json:"payloadFingerprint"`
	ActorType string `json:"actorType"`
	ActorIdentity string `json:"actorIdentity,omitempty"`
	ReasonCodes []string `json:"reasonCodes,omitempty"`
	SchemaVersion string `json:"schemaVersion"`
	LedgerFingerprint string `json:"ledgerFingerprint"`
}

func ledgerFingerprint(e paperLedgerEvent)string{e.LedgerFingerprint="";return fingerprint(e)}
func validLedgerEvent(e paperLedgerEvent)bool{return validPaperIdentifier(e.EventID)&&validPaperIdentifier(e.CorrelationID)&&ledgerEventTypes[e.EventType]&&!e.Timestamp.IsZero()&&e.SchemaVersion==operationsSchemaVersion&&e.PayloadFingerprint!=""&&e.CurrentStateFingerprint!=""&&e.LedgerFingerprint==ledgerFingerprint(e)&&(e.Pair==""||paperPairs[e.Pair])}

type reconciliationFinding struct {
	FindingID string `json:"findingId"`
	RunID string `json:"runId"`
	Severity string `json:"severity"`
	Reason string `json:"reason"`
	SubjectID string `json:"subjectId,omitempty"`
	EvidenceFingerprint string `json:"evidenceFingerprint"`
	DetectedAt time.Time `json:"detectedAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

type reconciliationRun struct {RunID string `json:"runId"`;StartedAt time.Time `json:"startedAt"`;CompletedAt time.Time `json:"completedAt"`;Outcome string `json:"outcome"`;EvidenceFingerprint string `json:"evidenceFingerprint"`;FindingCount int `json:"findingCount"`;Blocking bool `json:"blocking"`}

type operationsRiskState struct {StartingEquity float64 `json:"startingEquity"`;CurrentEquity float64 `json:"currentEquity"`;PeakEquity float64 `json:"peakEquity"`;OpenPositionCount int `json:"openPositionCount"`;PairExposure map[string]bool `json:"pairExposure"`;DailyRealizedPnL float64 `json:"dailyRealizedPnl"`;DailyLossPercent float64 `json:"dailyLossPercent"`;DrawdownPercent float64 `json:"drawdownPercent"`;TradingEnabled bool `json:"tradingEnabled"`;KillSwitchActive bool `json:"killSwitchActive"`}
type operationsEvidence struct {Orders []paperOrder `json:"orders"`;Positions []paperPosition `json:"positions"`;Journals map[string][]paperJournalEvent `json:"journals"`;Cycles []cycleRecord `json:"cycles"`;Ledger []paperLedgerEvent `json:"ledger"`;Risk operationsRiskState `json:"risk"`}

type dailyPaperRollup struct {Date string `json:"date"`;StartingEquity float64 `json:"startingEquity"`;EndingEquity float64 `json:"endingEquity"`;PeakEquity float64 `json:"peakEquity"`;RealizedPnL float64 `json:"realizedPnl"`;UnrealizedPnL float64 `json:"unrealizedPnl"`;GrossProfit float64 `json:"grossProfit"`;GrossLoss float64 `json:"grossLoss"`;TradingCosts float64 `json:"tradingCosts"`;NetPnL float64 `json:"netPnl"`;DailyLossPercent float64 `json:"dailyLossPercent"`;DrawdownPercent float64 `json:"drawdownPercent"`;TradesOpened int `json:"tradesOpened"`;TradesClosed int `json:"tradesClosed"`;Wins int `json:"wins"`;Losses int `json:"losses"`;Breakeven int `json:"breakeven"`;StopLossClosures int `json:"stopLossClosures"`;TakeProfitClosures int `json:"takeProfitClosures"`;NoTradeCount int `json:"noTradeCount"`;RejectionReasons map[string]int `json:"rejectionReasons"`;GroupCounts map[string]int `json:"groupCounts"`;EvidenceStart time.Time `json:"evidenceStart"`;EvidenceEnd time.Time `json:"evidenceEnd"`;SampleSize int `json:"sampleSize"`;Fingerprint string `json:"fingerprint"`}

type paperOperationsStore interface {Append(context.Context,paperLedgerEvent)(paperLedgerEvent,bool,error);Ledger(context.Context,int,int)([]paperLedgerEvent,error);SaveRun(context.Context,reconciliationRun,[]reconciliationFinding)error;Runs(context.Context,int)([]reconciliationRun,error);Findings(context.Context,bool,int)([]reconciliationFinding,error);Resolve(context.Context,string,time.Time)error;SaveRollup(context.Context,dailyPaperRollup)error;Rollup(context.Context,string)(dailyPaperRollup,error)}
type memoryPaperOperationsStore struct{mu sync.Mutex;events []paperLedgerEvent;eventByID map[string]paperLedgerEvent;runs []reconciliationRun;findings map[string]reconciliationFinding;rollups map[string]dailyPaperRollup}
func newMemoryPaperOperationsStore()*memoryPaperOperationsStore{return &memoryPaperOperationsStore{eventByID:map[string]paperLedgerEvent{},findings:map[string]reconciliationFinding{},rollups:map[string]dailyPaperRollup{}}}
func(s *memoryPaperOperationsStore)Append(_ context.Context,e paperLedgerEvent)(paperLedgerEvent,bool,error){s.mu.Lock();defer s.mu.Unlock();if old,ok:=s.eventByID[e.EventID];ok{if old.LedgerFingerprint!=e.LedgerFingerprint{return old,false,errors.New("ledger idempotency conflict")};return old,false,nil};expected:="GENESIS";if len(s.events)>0{expected=s.events[len(s.events)-1].LedgerFingerprint};if e.PreviousStateFingerprint!=expected||!validLedgerEvent(e){return paperLedgerEvent{},false,errors.New("invalid ledger chain")};s.events=append(s.events,e);s.eventByID[e.EventID]=e;return e,true,nil}
func(s *memoryPaperOperationsStore)Ledger(_ context.Context,offset,limit int)([]paperLedgerEvent,error){s.mu.Lock();defer s.mu.Unlock();if offset<0||limit<1||limit>100{return nil,errors.New("invalid pagination")};if offset>=len(s.events){return []paperLedgerEvent{},nil};end:=offset+limit;if end>len(s.events){end=len(s.events)};return append([]paperLedgerEvent(nil),s.events[offset:end]...),nil}
func(s *memoryPaperOperationsStore)SaveRun(_ context.Context,r reconciliationRun,fs []reconciliationFinding)error{s.mu.Lock();defer s.mu.Unlock();for _,old:=range s.runs{if old.RunID==r.RunID{if fingerprint(old)!=fingerprint(r){return errors.New("run conflict")};return nil}};s.runs=append(s.runs,r);for _,f:=range fs{s.findings[f.FindingID]=f};return nil}
func(s *memoryPaperOperationsStore)Runs(_ context.Context,n int)([]reconciliationRun,error){s.mu.Lock();defer s.mu.Unlock();if n<1||n>100{return nil,errors.New("invalid limit")};start:=len(s.runs)-n;if start<0{start=0};out:=append([]reconciliationRun(nil),s.runs[start:]...);sort.Slice(out,func(i,j int)bool{return out[i].StartedAt.After(out[j].StartedAt)});return out,nil}
func(s *memoryPaperOperationsStore)Findings(_ context.Context,active bool,n int)([]reconciliationFinding,error){s.mu.Lock();defer s.mu.Unlock();if n<1||n>100{return nil,errors.New("invalid limit")};out:=[]reconciliationFinding{};for _,f:=range s.findings{if !active||f.ResolvedAt==nil{out=append(out,f)}};sort.Slice(out,func(i,j int)bool{if out[i].DetectedAt.Equal(out[j].DetectedAt){return out[i].FindingID<out[j].FindingID};return out[i].DetectedAt.Before(out[j].DetectedAt)});if len(out)>n{out=out[:n]};return out,nil}
func(s *memoryPaperOperationsStore)Resolve(_ context.Context,id string,at time.Time)error{s.mu.Lock();defer s.mu.Unlock();f,ok:=s.findings[id];if !ok{return errors.New("finding not found")};if f.ResolvedAt==nil{v:=at.UTC();f.ResolvedAt=&v;s.findings[id]=f};return nil}
func(s *memoryPaperOperationsStore)SaveRollup(_ context.Context,r dailyPaperRollup)error{s.mu.Lock();defer s.mu.Unlock();s.rollups[r.Date]=r;return nil}
func(s *memoryPaperOperationsStore)Rollup(_ context.Context,d string)(dailyPaperRollup,error){s.mu.Lock();defer s.mu.Unlock();v,ok:=s.rollups[d];if !ok{return v,errors.New("rollup not found")};return v,nil}

type reconciliationLocker interface {AcquireReconciliation(context.Context,string,time.Duration)(func(context.Context)error,error)}
type paperOperations struct {mu sync.RWMutex;store paperOperationsStore;now func()time.Time;ready bool;paused bool;blocking bool;killSwitch bool;degradedReason string;lastRun *reconciliationRun;evidence func(context.Context)(operationsEvidence,error);stop chan struct{};stopOnce sync.Once;workers sync.WaitGroup}
func newPaperOperations(store paperOperationsStore,now func()time.Time,evidence func(context.Context)(operationsEvidence,error))*paperOperations{return &paperOperations{store:store,now:now,evidence:evidence,stop:make(chan struct{})}}
func(o *paperOperations)acceptanceGate(context.Context,paperAutomationRequest)string{o.mu.RLock();defer o.mu.RUnlock();if o.killSwitch{return "KILL_SWITCH_ACTIVE"};if o.blocking{return "RECONCILIATION_BLOCKING"};if !o.ready{return "RECONCILIATION_REQUIRED"};if o.paused{return "RUNTIME_PAUSED"};return ""}
func(o *paperOperations)status()map[string]any{o.mu.RLock();defer o.mu.RUnlock();dependency:="available";if o.degradedReason!=""{dependency="unavailable"};return map[string]any{"health":"alive","ready":o.ready,"paused":o.paused,"killSwitchActive":o.killSwitch,"blockingFindings":o.blocking,"degradedReason":o.degradedReason,"dependencies":map[string]string{"operationsStore":dependency,"evidence":dependency},"lastReconciliation":o.lastRun,"warning":operationsWarning}}

func makeLedgerEvent(previous,eventID,correlation,cycle,pair,frame,strategy,version,eventType string,at time.Time,payload any,actorType,actor string,reasons []string)paperLedgerEvent{e:=paperLedgerEvent{EventID:eventID,CorrelationID:correlation,CycleID:cycle,Pair:pair,Timeframe:frame,Strategy:strategy,StrategyVersion:version,EventType:eventType,Timestamp:at.UTC(),PreviousStateFingerprint:previous,CurrentStateFingerprint:fingerprint(payload),PayloadFingerprint:fingerprint(payload),ActorType:actorType,ActorIdentity:actor,ReasonCodes:append([]string(nil),reasons...),SchemaVersion:operationsSchemaVersion};e.LedgerFingerprint=ledgerFingerprint(e);return e}

func finding(runID,severity,reason,subject string,evidence any,at time.Time)reconciliationFinding{id:="finding-"+strings.TrimPrefix(fingerprint(struct{Run,Reason,Subject string}{runID,reason,subject}),"sha256:");return reconciliationFinding{FindingID:id,RunID:runID,Severity:severity,Reason:reason,SubjectID:subject,EvidenceFingerprint:fingerprint(evidence),DetectedAt:at.UTC()}}

func reconcileEvidence(runID string,e operationsEvidence,at time.Time)[]reconciliationFinding{fs:=[]reconciliationFinding{};orders:=map[string]paperOrder{};positions:=map[string][]paperPosition{};openByPair:=map[string]bool{};openCount:=0;realized:=0.0
	for _,o:=range e.Orders{orders[o.OrderID]=o;if o.Mode!="PAPER"||!paperPairs[o.Pair]||!paperStrategies[o.Strategy]{fs=append(fs,finding(runID,"CRITICAL","UNSUPPORTED_OR_LIVE_ORDER",o.OrderID,o,at))}}
	for _,p:=range e.Positions{positions[p.OrderID]=append(positions[p.OrderID],p);if _,ok:=orders[p.OrderID];!ok{fs=append(fs,finding(runID,"CRITICAL","POSITION_WITHOUT_ORDER",p.OrderID,p,at))};if !paperPairs[p.Pair]||!paperStrategies[p.Strategy]{fs=append(fs,finding(runID,"CRITICAL","UNSUPPORTED_POSITION_IDENTITY",p.OrderID,p,at))};if p.Status=="OPEN"{openCount++;openByPair[p.Pair]=true}else if p.Status=="CLOSED"{realized+=p.RealizedPnL}else{fs=append(fs,finding(runID,"BLOCKING","INVALID_POSITION_STATUS",p.OrderID,p,at))};events:=e.Journals[p.OrderID];if len(events)==0{fs=append(fs,finding(runID,"BLOCKING","MISSING_LIFECYCLE_EVENTS",p.OrderID,p,at))}else{closed:=0;for i,event:=range events{if i>0&&event.RecordedAt.Before(events[i-1].RecordedAt){fs=append(fs,finding(runID,"BLOCKING","INVALID_TRANSITION_ORDER",p.OrderID,events,at));break};if strings.HasPrefix(event.Type,"POSITION_CLOSED_"){closed++}};if p.Status=="CLOSED"&&closed==0{fs=append(fs,finding(runID,"BLOCKING","MISSING_CLOSURE_EVENT",p.OrderID,events,at))};if closed>1{fs=append(fs,finding(runID,"CRITICAL","CONFLICTING_CLOSURE_EVENTS",p.OrderID,events,at))};if p.Status=="OPEN"&&closed>0{fs=append(fs,finding(runID,"CRITICAL","CLOSED_POSITION_COUNTED_OPEN",p.OrderID,events,at))}}}
	for id,o:=range orders{if o.Status=="PAPER_ACCEPTED"&&len(positions[id])==0{fs=append(fs,finding(runID,"BLOCKING","ORDER_WITHOUT_POSITION",id,o,at))};if len(positions[id])>1{fs=append(fs,finding(runID,"CRITICAL","DUPLICATE_POSITION",id,positions[id],at))}}
	for pair,marked:=range e.Risk.PairExposure{if marked&&!openByPair[pair]{fs=append(fs,finding(runID,"BLOCKING","ORPHAN_PAIR_EXPOSURE",pair,e.Risk.PairExposure,at))}};for pair:=range openByPair{if !e.Risk.PairExposure[pair]{fs=append(fs,finding(runID,"BLOCKING","MISSING_PAIR_EXPOSURE",pair,e.Risk.PairExposure,at))}}
	if e.Risk.OpenPositionCount!=openCount{fs=append(fs,finding(runID,"BLOCKING","OPEN_POSITION_COUNT_MISMATCH","risk",struct{Expected,Actual int}{openCount,e.Risk.OpenPositionCount},at))};if round(e.Risk.DailyRealizedPnL,8)!=round(realized,8){fs=append(fs,finding(runID,"BLOCKING","REALIZED_PNL_MISMATCH","risk",struct{Expected,Actual float64}{realized,e.Risk.DailyRealizedPnL},at))};expectedLoss:=0.0;if e.Risk.StartingEquity>0&&realized<0{expectedLoss=-realized/e.Risk.StartingEquity*100};if round(expectedLoss,6)!=round(e.Risk.DailyLossPercent,6){fs=append(fs,finding(runID,"BLOCKING","DAILY_LOSS_MISMATCH","risk",expectedLoss,at))};expectedDrawdown:=0.0;if e.Risk.PeakEquity>0{expectedDrawdown=(e.Risk.PeakEquity-e.Risk.CurrentEquity)/e.Risk.PeakEquity*100};if round(expectedDrawdown,6)!=round(e.Risk.DrawdownPercent,6){fs=append(fs,finding(runID,"BLOCKING","DRAWDOWN_MISMATCH","risk",expectedDrawdown,at))}
	previous:="GENESIS";for _,event:=range e.Ledger{if event.PreviousStateFingerprint!=previous||!validLedgerEvent(event){fs=append(fs,finding(runID,"CRITICAL","BROKEN_LEDGER_CHAIN",event.EventID,event,at));break};previous=event.LedgerFingerprint};return fs}

func(o *paperOperations)reconcile(ctx context.Context,trigger string)(reconciliationRun,[]reconciliationFinding,error){if o.evidence==nil{return reconciliationRun{},nil,errors.New("evidence unavailable")};started:=o.now().UTC();owner:="reconcile-"+strings.TrimPrefix(fingerprint(struct{Trigger string;At time.Time}{trigger,started}),"sha256:");if locker,ok:=o.store.(reconciliationLocker);ok{release,err:=locker.AcquireReconciliation(ctx,owner,30*time.Second);if err!=nil{o.failClosed("RECONCILIATION_LOCK_UNAVAILABLE");return reconciliationRun{},nil,err};defer func(){_ = release(context.Background())}()};e,err:=o.evidence(ctx);if err!=nil{o.failClosed("EVIDENCE_UNAVAILABLE");return reconciliationRun{},nil,err};runID:="reconcile-"+strings.TrimPrefix(fingerprint(struct{At time.Time;Evidence string}{started,fingerprint(e)}),"sha256:");fs:=reconcileEvidence(runID,e,started);blocking:=false;critical:=false;for _,f:=range fs{if f.Severity=="BLOCKING"||f.Severity=="CRITICAL"{blocking=true};if f.Severity=="CRITICAL"{critical=true};reason:=f.Reason;if !findingReasons[reason]{reason="OTHER"};operationsEvents.WithLabelValues("finding",f.Severity,reason).Inc()};completed:=o.now().UTC();outcome:="CONSISTENT";if blocking{outcome="INCONSISTENT"};run:=reconciliationRun{RunID:runID,StartedAt:started,CompletedAt:completed,Outcome:outcome,EvidenceFingerprint:fingerprint(e),FindingCount:len(fs),Blocking:blocking};if err=o.store.SaveRun(ctx,run,fs);err!=nil{o.failClosed("OPERATIONS_STORE_UNAVAILABLE");return reconciliationRun{},nil,err};rollup:=buildDailyRollup(started.Format("2006-01-02"),e);if err=o.store.SaveRollup(ctx,rollup);err!=nil{o.failClosed("ROLLUP_STORE_UNAVAILABLE");return reconciliationRun{},nil,err};o.mu.Lock();o.lastRun=&run;o.blocking=blocking;o.killSwitch=o.killSwitch||critical;o.ready=!blocking&&!critical;o.degradedReason="";if blocking{o.degradedReason="RECONCILIATION_FINDINGS"};o.mu.Unlock();if o.ready{operationsReady.Set(1)}else{operationsReady.Set(0)};operationsEvents.WithLabelValues("reconciliation","INFO",outcome).Inc();return run,fs,nil}

func(o *paperOperations)failClosed(reason string){o.mu.Lock();o.ready=false;o.blocking=true;o.degradedReason=reason;o.mu.Unlock();operationsReady.Set(0)}
func(o *paperOperations)Start(parent context.Context,interval time.Duration){if interval<=0{return};o.workers.Add(1);go func(){defer o.workers.Done();ticker:=time.NewTicker(interval);defer ticker.Stop();for{select{case<-parent.Done():return;case<-o.stop:return;case<-ticker.C:ctx,cancel:=context.WithTimeout(parent,30*time.Second);_,_,_ = o.reconcile(ctx,"periodic");cancel()}}}()}
func(o *paperOperations)Stop(){o.stopOnce.Do(func(){close(o.stop)});o.workers.Wait()}

func(o *paperOperations)ledgerTail(ctx context.Context)(string,error){previous:="GENESIS";for offset:=0;offset<1000;offset+=100{page,err:=o.store.Ledger(ctx,offset,100);if err!=nil{return "",err};for _,event:=range page{if event.PreviousStateFingerprint!=previous||!validLedgerEvent(event){return "",errors.New("invalid ledger chain")};previous=event.LedgerFingerprint};if len(page)<100{return previous,nil}};return "",errors.New("ledger append bound exceeded")}
func(o *paperOperations)appendEvent(ctx context.Context,eventID,correlation,cycle,pair,frame,strategy,version,eventType string,payload any,reasons []string)error{previous,err:=o.ledgerTail(ctx);if err!=nil{return err};event:=makeLedgerEvent(previous,eventID,correlation,cycle,pair,frame,strategy,version,eventType,o.now(),payload,"SYSTEM","",reasons);_,_,err=o.store.Append(ctx,event);return err}
func(o *paperOperations)recordAccepted(ctx context.Context,input paperAutomationRequest,setup automationSetup,order paperOrder,position paperPosition)error{if err:=o.appendEvent(ctx,"ledger-order-"+input.RequestID,input.RequestID,input.RequestID,setup.Pair,setup.Timeframe,setup.Strategy,setup.StrategyVersion,"PAPER_ORDER_ACCEPTED",order,nil);err!=nil{o.failClosed("LEDGER_UNAVAILABLE");return err};if err:=o.appendEvent(ctx,"ledger-position-"+input.RequestID,input.RequestID,input.RequestID,setup.Pair,setup.Timeframe,setup.Strategy,setup.StrategyVersion,"PAPER_POSITION_OPENED",position,nil);err!=nil{o.failClosed("LEDGER_UNAVAILABLE");return err};return nil}
func(o *paperOperations)recordCycle(ctx context.Context,record cycleRecord)error{eventType:="RUNTIME_CYCLE_EVALUATED";if record.Outcome=="NO_TRADE"{eventType="NO_TRADE"};if err:=o.appendEvent(ctx,"ledger-cycle-"+record.Cycle.ID,record.Cycle.ID,record.Cycle.ID,record.Cycle.Pair,record.Cycle.Timeframe,"","",eventType,record,record.Reasons);err!=nil{o.failClosed("LEDGER_UNAVAILABLE");return err};return nil}

func buildDailyRollup(date string,e operationsEvidence)dailyPaperRollup{r:=dailyPaperRollup{Date:date,StartingEquity:e.Risk.StartingEquity,EndingEquity:e.Risk.CurrentEquity,PeakEquity:e.Risk.PeakEquity,RealizedPnL:e.Risk.DailyRealizedPnL,DailyLossPercent:e.Risk.DailyLossPercent,DrawdownPercent:e.Risk.DrawdownPercent,RejectionReasons:map[string]int{},GroupCounts:map[string]int{}};for _,p:=range e.Positions{r.SampleSize++;if r.EvidenceStart.IsZero()||p.OpenedAt.Before(r.EvidenceStart){r.EvidenceStart=p.OpenedAt};end:=p.OpenedAt;if p.ClosedAt!=nil{end=*p.ClosedAt};if end.After(r.EvidenceEnd){r.EvidenceEnd=end};r.TradesOpened++;r.GroupCounts[p.Pair+"/"+p.Strategy]++;if p.Status=="OPEN"{r.UnrealizedPnL+=p.UnrealizedPnL}else{r.TradesClosed++;if p.RealizedPnL>0{r.Wins++;r.GrossProfit+=p.RealizedPnL}else if p.RealizedPnL<0{r.Losses++;r.GrossLoss+=p.RealizedPnL}else{r.Breakeven++};if p.CloseReason=="STOP_LOSS"{r.StopLossClosures++};if p.CloseReason=="TAKE_PROFIT"{r.TakeProfitClosures++}}};for _,c:=range e.Cycles{if c.Outcome=="NO_TRADE"{r.NoTradeCount++;for _,reason:=range c.Reasons{r.RejectionReasons[reason]++}}};r.NetPnL=r.RealizedPnL+r.UnrealizedPnL-r.TradingCosts;r.Fingerprint="";r.Fingerprint=fingerprint(r);return r}

type operatorCommand struct {RequestID string `json:"requestId"`;Action string `json:"action"`;Reason string `json:"reason"`;ActorID string `json:"actorId"`;Authorized bool `json:"-"`}
func(o *paperOperations)executeOperatorCommand(context.Context,operatorCommand)error{return errors.New("OPERATOR_AUTHENTICATION_BOUNDARY_UNAVAILABLE")}

func(o *paperOperations)statusHandler(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodGet{jsonResponse(w,http.StatusMethodNotAllowed,map[string]any{"error":"method_not_allowed"});return};jsonResponse(w,http.StatusOK,map[string]any{"operations":o.status()})}
func(o *paperOperations)findingsHandler(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodGet{jsonResponse(w,http.StatusMethodNotAllowed,map[string]any{"error":"method_not_allowed"});return};limit:=50;items,err:=o.store.Findings(r.Context(),true,limit);if err!=nil{jsonResponse(w,http.StatusServiceUnavailable,map[string]any{"error":"operations_unavailable"});return};jsonResponse(w,http.StatusOK,map[string]any{"findings":items,"limit":limit})}
func(o *paperOperations)ledgerHandler(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodGet{jsonResponse(w,http.StatusMethodNotAllowed,map[string]any{"error":"method_not_allowed"});return};items,err:=o.store.Ledger(r.Context(),0,50);if err!=nil{jsonResponse(w,http.StatusServiceUnavailable,map[string]any{"error":"operations_unavailable"});return};jsonResponse(w,http.StatusOK,map[string]any{"events":items,"limit":50})}
func(o *paperOperations)rollupHandler(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodGet{jsonResponse(w,http.StatusMethodNotAllowed,map[string]any{"error":"method_not_allowed"});return};date:=strings.TrimSpace(r.URL.Query().Get("date"));if len(date)!=10{jsonResponse(w,http.StatusBadRequest,map[string]any{"error":"invalid_date"});return};v,err:=o.store.Rollup(r.Context(),date);if err!=nil{jsonResponse(w,http.StatusNotFound,map[string]any{"error":"not_found"});return};jsonResponse(w,http.StatusOK,map[string]any{"rollup":v})}

func validateNoLiveOperationsSurface()error{for _,v:=range []string{"LIVE","BROKER_ORDER","BROKER_POSITION"}{if ledgerEventTypes[v]{return fmt.Errorf("prohibited operations event %s",v)}};return nil}
