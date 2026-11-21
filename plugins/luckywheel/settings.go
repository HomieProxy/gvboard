package luckywheel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	sdk "gvboard/pkg/plugin"
)

// 奖品类型。
const (
	PrizeBalance = "balance" // 余额（分）
	PrizeDays    = "days"    // 到期时间（天）
	PrizeTraffic = "traffic" // 本周期流量（MB）
	PrizeNone    = "none"    // 谢谢惠顾
)

// 消耗方式。
const (
	CostFree    = "free"
	CostBalance = "balance"
	CostTraffic = "traffic"
)

const (
	mb = int64(1048576)

	maxPrizes       = 12
	minPrizes       = 2
	maxPrizeName    = 16
	maxWeight       = 1000000
	maxFreeDaily    = 100
	maxPaidDaily    = 1000
	maxBalanceValue = 10000000 // 10 万元
	maxDaysValue    = 3650
	maxTrafficValue = 10485760 // 10 TB
	maxBalanceCost  = 10000000
	maxTrafficCost  = 1048576 // 1 TB
	maxRetention    = 3650

	settingsKey = "settings"
)

// Prize 奖品。Value 单位：余额为分、到期时间为天、流量为 MB，谢谢惠顾为 0。
type Prize struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Value  int64  `json:"value"`
	Weight int64  `json:"weight"`
}

// Settings 转盘配置（存于插件 KV）。
type Settings struct {
	FreeDaily     int64   `json:"free_daily"`
	BalanceEnable bool    `json:"balance_enable"`
	BalanceCost   int64   `json:"balance_cost"` // 分
	TrafficEnable bool    `json:"traffic_enable"`
	TrafficCost   int64   `json:"traffic_cost"` // MB
	PaidDaily     int64   `json:"paid_daily"`   // 0 不限
	RetentionDays int64   `json:"retention_days"`
	Prizes        []Prize `json:"prizes"`
}

func defaults() Settings {
	return Settings{
		FreeDaily:     1,
		BalanceCost:   100,
		TrafficCost:   1024,
		RetentionDays: 90,
		Prizes: []Prize{
			{Name: "谢谢惠顾", Type: PrizeNone, Weight: 50},
			{Name: "0.1 元余额", Type: PrizeBalance, Value: 10, Weight: 30},
			{Name: "1 GB 流量", Type: PrizeTraffic, Value: 1024, Weight: 15},
			{Name: "1 天时长", Type: PrizeDays, Value: 1, Weight: 5},
		},
	}
}

func (s Settings) validate() error {
	switch {
	case s.FreeDaily < 0 || s.FreeDaily > maxFreeDaily:
		return fmt.Errorf("每日免费次数需为 0～%d", maxFreeDaily)
	case s.PaidDaily < 0 || s.PaidDaily > maxPaidDaily:
		return fmt.Errorf("每日付费次数上限需为 0～%d", maxPaidDaily)
	case s.BalanceEnable && (s.BalanceCost < 1 || s.BalanceCost > maxBalanceCost):
		return errors.New("每次消耗余额需为 0.01～100000 元")
	case s.TrafficEnable && (s.TrafficCost < 1 || s.TrafficCost > maxTrafficCost):
		return fmt.Errorf("每次消耗流量需为 1～%d MB", maxTrafficCost)
	case s.RetentionDays < 1 || s.RetentionDays > maxRetention:
		return fmt.Errorf("记录保留天数需为 1～%d", maxRetention)
	case len(s.Prizes) < minPrizes || len(s.Prizes) > maxPrizes:
		return fmt.Errorf("奖品数量需为 %d～%d 个", minPrizes, maxPrizes)
	}
	var total int64
	for i, p := range s.Prizes {
		n := utf8.RuneCountInString(p.Name)
		if n < 1 || n > maxPrizeName {
			return fmt.Errorf("第 %d 个奖品名称需为 1～%d 个字", i+1, maxPrizeName)
		}
		if p.Weight < 0 || p.Weight > maxWeight {
			return fmt.Errorf("第 %d 个奖品权重需为 0～%d", i+1, maxWeight)
		}
		limit := map[string]int64{PrizeBalance: maxBalanceValue, PrizeDays: maxDaysValue, PrizeTraffic: maxTrafficValue, PrizeNone: 0}
		hi, ok := limit[p.Type]
		if !ok {
			return fmt.Errorf("第 %d 个奖品类型无效", i+1)
		}
		if p.Type == PrizeNone && p.Value != 0 || p.Type != PrizeNone && (p.Value < 1 || p.Value > hi) {
			return fmt.Errorf("第 %d 个奖品数值超出范围", i+1)
		}
		total += p.Weight
	}
	if total <= 0 {
		return errors.New("奖品权重总和需大于 0")
	}
	return nil
}

// load 读取配置，没有保存过时为默认配置。
func load(ctx context.Context, h sdk.Host) (Settings, error) {
	v, err := h.KV().Get(ctx, settingsKey)
	if errors.Is(err, sdk.ErrKVNotFound) {
		return defaults(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return Settings{}, err
	}
	return s, s.validate()
}

func save(ctx context.Context, h sdk.Host, s Settings) error {
	if err := s.validate(); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return h.KV().Set(ctx, settingsKey, string(b), 0)
}

// yuan 分转换为元，如 5 → "0.05"。
func yuan(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

// traffic MB 转换为易读文本，如 1536 → "1.5 GB"。
func traffic(m int64) string {
	switch {
	case m >= 1048576:
		return trimZero(float64(m)/1048576) + " TB"
	case m >= 1024:
		return trimZero(float64(m)/1024) + " GB"
	}
	return fmt.Sprintf("%d MB", m)
}

func trimZero(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	for s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

// describe 奖品内容，如「余额 0.10 元」。
func (p Prize) describe() string {
	switch p.Type {
	case PrizeBalance:
		return "余额 " + yuan(p.Value) + " 元"
	case PrizeDays:
		return fmt.Sprintf("订阅时长 %d 天", p.Value)
	case PrizeTraffic:
		return "本周期流量 " + traffic(p.Value)
	}
	return ""
}
