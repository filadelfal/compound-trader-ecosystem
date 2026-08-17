package main

import "math"

type positionSizeRequest struct {
	RequestID              string  `json:"requestId"`
	Pair                   string  `json:"pair"`
	Direction              string  `json:"direction"`
	Equity                 float64 `json:"equity"`
	RiskPercent            float64 `json:"riskPercent"`
	Entry                  float64 `json:"entry"`
	StopLoss               float64 `json:"stopLoss"`
	PipValuePerStandardLot float64 `json:"pipValuePerStandardLot"`
	MinimumLot             float64 `json:"minimumLot"`
	MaximumLot             float64 `json:"maximumLot"`
	LotStep                float64 `json:"lotStep"`
	DailyLossPercent       float64 `json:"dailyLossPercent"`
	DrawdownPercent        float64 `json:"drawdownPercent"`
	OpenPositions          int     `json:"openPositions"`
	ExistingPairExposure   bool    `json:"existingPairExposure"`
	TradingEnabled         bool    `json:"tradingEnabled"`
	KillSwitchActive       bool    `json:"killSwitchActive"`
}

type positionSizeResult struct {
	Outcome          string   `json:"outcome"`
	RequestID        string   `json:"requestId,omitempty"`
	Reasons          []string `json:"reasons"`
	RiskAmount       float64  `json:"riskAmount,omitempty"`
	StopDistancePips float64  `json:"stopDistancePips,omitempty"`
	Lots             float64  `json:"lots,omitempty"`
}

func pipSize(pair string) (float64, bool) {
	switch pair {
	case "EURUSD", "GBPUSD":
		return 0.0001, true
	case "USDJPY":
		return 0.01, true
	default:
		return 0, false
	}
}

func round(value float64, places int) float64 {
	scale := math.Pow10(places)
	return math.Round(value*scale) / scale
}

func calculatePositionSize(input positionSizeRequest) positionSizeResult {
	rejected := func(reasons ...string) positionSizeResult {
		return positionSizeResult{Outcome: "NO_TRADE", RequestID: input.RequestID, Reasons: reasons}
	}

	pip, supported := pipSize(input.Pair)
	if input.RequestID == "" || !supported || (input.Direction != "BUY" && input.Direction != "SELL") ||
		input.Equity <= 0 || input.RiskPercent <= 0 || input.RiskPercent > 1 ||
		input.Entry <= 0 || input.StopLoss <= 0 || input.PipValuePerStandardLot <= 0 ||
		input.MinimumLot <= 0 || input.MaximumLot < input.MinimumLot || input.LotStep <= 0 ||
		input.DailyLossPercent < 0 || input.DrawdownPercent < 0 || input.OpenPositions < 0 {
		return rejected("INVALID_INPUT")
	}
	if !input.TradingEnabled {
		return rejected("TRADING_DISABLED")
	}
	if input.KillSwitchActive {
		return rejected("KILL_SWITCH_ACTIVE")
	}
	if input.DailyLossPercent >= 2 {
		return rejected("DAILY_LOSS_LIMIT_REACHED")
	}
	if input.DrawdownPercent >= 5 {
		return rejected("DRAWDOWN_LIMIT_REACHED")
	}
	if input.OpenPositions >= 2 {
		return rejected("POSITION_LIMIT_REACHED")
	}
	if input.ExistingPairExposure {
		return rejected("PAIR_EXPOSURE_EXISTS")
	}
	if (input.Direction == "BUY" && input.StopLoss >= input.Entry) ||
		(input.Direction == "SELL" && input.StopLoss <= input.Entry) {
		return rejected("INVALID_STOP_STRUCTURE")
	}

	stopDistancePips := math.Abs(input.Entry-input.StopLoss) / pip
	riskAmount := input.Equity * input.RiskPercent / 100
	rawLots := riskAmount / (stopDistancePips * input.PipValuePerStandardLot)
	lots := math.Floor((rawLots+1e-12)/input.LotStep) * input.LotStep
	if lots > input.MaximumLot {
		lots = math.Floor((input.MaximumLot+1e-12)/input.LotStep) * input.LotStep
	}
	if lots < input.MinimumLot {
		return rejected("SIZE_BELOW_BROKER_MINIMUM")
	}

	return positionSizeResult{
		Outcome:          "RISK_APPROVED",
		RequestID:        input.RequestID,
		Reasons:          []string{},
		RiskAmount:       round(riskAmount, 2),
		StopDistancePips: round(stopDistancePips, 2),
		Lots:             round(lots, 8),
	}
}
