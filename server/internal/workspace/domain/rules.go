package domain

import (
	"fmt"
	"strings"
	"time"
)

// Rule thresholds. Will be editable in the admin back office.
const (
	PriceTolerance  = 0.05           // price increase Fitkit covers silently
	ConfirmWindow   = 24 * time.Hour // client has this long to confirm a higher price
	NoTrackingAfter = 72 * time.Hour // store parcel without tracking → problem
	NoMovementAfter = 5 * 24 * time.Hour
)

// Due times set on status changes.
var DueAfter = map[OrderStatus]time.Duration{
	StatusNew:        30 * time.Minute,
	StatusInProgress: 6 * time.Hour,
	StatusAwaiting:   ConfirmWindow,
	StatusPurchased:  NoTrackingAfter,
	StatusShipped:    72 * time.Hour,
}

type RuleKind string

const (
	RuleCovered RuleKind = "covered"
	RuleConfirm RuleKind = "confirm"
	RuleSoldOut RuleKind = "sold_out"
)

type Rule struct {
	Kind    RuleKind
	Covered Money // extra amount Fitkit pays
}

// RuleFor returns the rule that applies to an item, if any.
func RuleFor(it Item) *Rule {
	if it.Status == ItemSoldOut {
		return &Rule{Kind: RuleSoldOut}
	}
	if it.Price != nil && it.StorePrice != nil && *it.StorePrice > *it.Price && *it.Price > 0 {
		diff := (*it.StorePrice - *it.Price) * Money(it.Qty)
		pct := float64(*it.StorePrice-*it.Price) / float64(*it.Price)
		if pct <= PriceTolerance+1e-9 {
			return &Rule{Kind: RuleCovered, Covered: diff}
		}
		return &Rule{Kind: RuleConfirm}
	}
	return nil
}

// IsBlocking reports whether an item still blocks “Purchased”.
func IsBlocking(it Item) bool {
	switch it.Status {
	case ItemCancelled, ItemBought:
		return false
	case ItemPending, ItemSoldOut, ItemAwaiting:
		return true
	}
	r := RuleFor(it)
	return r != nil && r.Kind == RuleConfirm
}

func CanMarkPurchased(o Order) bool {
	alive := false
	for _, it := range o.Items {
		if IsBlocking(it) {
			return false
		}
		if it.Status != ItemCancelled {
			alive = true
		}
	}
	return alive
}

// ClientStatus is the only status text the client ever sees.
func ClientStatus(o Order) string {
	switch o.Status {
	case StatusNew:
		return "Замовлення підтверджено"
	case StatusInProgress, StatusPurchased:
		return "Готуємо ваше замовлення"
	case StatusAwaiting:
		for _, it := range o.Items {
			if it.Status == ItemAwaiting {
				return "Ціна змінилась (потрібна дія)"
			}
		}
		return "Готуємо ваше замовлення"
	case StatusShipped:
		return "Відправлено"
	case StatusDelivered:
		return "Доставлено"
	case StatusCancelled:
		return "Скасовано, кошти повернуто"
	}
	return ""
}

type MoneySummary struct {
	Paid       Money `json:"paid"`
	Refunds    Money `json:"refunds"`
	Fee        Money `json:"fee"`
	Covered    Money `json:"covered"`
	ToBuy      Money `json:"toBuy"`
	PriceKnown bool  `json:"priceKnown"`
}

func Summarize(o Order) MoneySummary {
	m := MoneySummary{Fee: o.Fee, PriceKnown: o.Paid != nil}
	if o.Paid != nil {
		m.Paid = *o.Paid
	}
	for _, it := range o.Items {
		var price Money
		if it.Price != nil {
			price = *it.Price
		} else {
			m.PriceKnown = false
		}
		switch it.Status {
		case ItemCancelled:
			m.Refunds += price * Money(it.Qty)
			continue
		case ItemSoldOut:
			continue
		}
		if r := RuleFor(it); r != nil && r.Kind == RuleCovered {
			m.Covered += r.Covered
		}
		unit := price
		if it.StorePrice != nil {
			unit = *it.StorePrice
		}
		m.ToBuy += unit * Money(it.Qty)
	}
	return m
}

var StatusLabel = map[OrderStatus]string{
	StatusNew:        "Нова",
	StatusInProgress: "В роботі",
	StatusAwaiting:   "Чекає підтвердження",
	StatusPurchased:  "Викуплено",
	StatusShipped:    "Відправлено",
	StatusDelivered:  "Доставлено",
	StatusCancelled:  "Скасовано",
}

// UAH formats kopiyky as “1 799 ₴” (or “1 799,50 ₴”).
func UAH(m Money) string {
	neg := m < 0
	if neg {
		m = -m
	}
	whole, kop := m/100, m%100
	s := fmt.Sprint(whole)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if kop != 0 {
		out += fmt.Sprintf(",%02d", kop)
	}
	if neg {
		out = "−" + out
	}
	return out + " ₴"
}
