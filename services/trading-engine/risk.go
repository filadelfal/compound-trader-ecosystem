package main

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var signedDecimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]{1,8})?$`)

type RiskPolicy struct {
	MaxOrderQuantity string
	MaxOrderNotional string
}

func DefaultRiskPolicy() RiskPolicy {
	return RiskPolicy{
		MaxOrderQuantity: "1000000",
		MaxOrderNotional: "10000000",
	}
}

func (policy RiskPolicy) Validate(input CreateOrderInput) error {
	quantity, err := decimalRat(input.Quantity)
	if err != nil {
		return err
	}
	maxQuantity, err := decimalRat(policy.MaxOrderQuantity)
	if err != nil {
		return fmt.Errorf("invalid max order quantity configuration: %w", err)
	}
	if quantity.Cmp(maxQuantity) > 0 {
		return fmt.Errorf("order quantity exceeds the configured maximum of %s", policy.MaxOrderQuantity)
	}

	if input.LimitPrice != nil {
		price, parseErr := decimalRat(*input.LimitPrice)
		if parseErr != nil {
			return parseErr
		}
		maxNotional, parseErr := decimalRat(policy.MaxOrderNotional)
		if parseErr != nil {
			return fmt.Errorf("invalid max order notional configuration: %w", parseErr)
		}
		notional := new(big.Rat).Mul(quantity, price)
		if notional.Cmp(maxNotional) > 0 {
			return fmt.Errorf("order notional exceeds the configured maximum of %s", policy.MaxOrderNotional)
		}
	}
	return nil
}

func decimalRat(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if !positiveDecimal(value) {
		return nil, errors.New("value must be a positive decimal")
	}
	result, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, errors.New("value must be a valid decimal")
	}
	return result, nil
}

func decimalRatAllowZero(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if !decimalPattern.MatchString(value) {
		return nil, errors.New("value must be a non-negative decimal")
	}
	result, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, errors.New("value must be a valid decimal")
	}
	return result, nil
}

func signedDecimalRat(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if !signedDecimalPattern.MatchString(value) {
		return nil, errors.New("value must be a signed decimal")
	}
	result, ok := new(big.Rat).SetString(value)
	if !ok || result.Sign() == 0 {
		return nil, errors.New("value must be a non-zero decimal")
	}
	return result, nil
}

func signedOrZeroDecimalRat(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if !signedDecimalPattern.MatchString(value) {
		return nil, errors.New("value must be a signed decimal")
	}
	result, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, errors.New("value must be a valid decimal")
	}
	return result, nil
}
