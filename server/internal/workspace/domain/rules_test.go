package domain

import "testing"

func p(v Money) *Money { return &v }

func TestRuleFor(t *testing.T) {
	cases := []struct {
		name string
		it   Item
		want RuleKind
		cov  Money
	}{
		{"sold out", Item{Status: ItemSoldOut, Qty: 1, Price: p(399900)}, RuleSoldOut, 0},
		{"+3.6% covered", Item{Status: ItemChecked, Qty: 1, Price: p(139900), StorePrice: p(144900)}, RuleCovered, 5000},
		{"exactly 5% covered", Item{Status: ItemChecked, Qty: 2, Price: p(100000), StorePrice: p(105000)}, RuleCovered, 10000},
		{"+12% confirm", Item{Status: ItemChecked, Qty: 1, Price: p(589900), StorePrice: p(659900)}, RuleConfirm, 0},
		{"cheaper — no rule", Item{Status: ItemChecked, Qty: 1, Price: p(100000), StorePrice: p(90000)}, "", 0},
		{"unknown price — no rule", Item{Status: ItemChecked, Qty: 1, StorePrice: p(90000)}, "", 0},
	}
	for _, c := range cases {
		r := RuleFor(c.it)
		if c.want == "" {
			if r != nil {
				t.Errorf("%s: want no rule, got %v", c.name, r.Kind)
			}
			continue
		}
		if r == nil || r.Kind != c.want || r.Covered != c.cov {
			t.Errorf("%s: want %s/%d, got %+v", c.name, c.want, c.cov, r)
		}
	}
}

func TestCanMarkPurchased(t *testing.T) {
	o := Order{Items: []Item{
		{Status: ItemChecked, Qty: 1, Price: p(179900)},
		{Status: ItemSoldOut, Qty: 1, Price: p(399900)},
	}}
	if CanMarkPurchased(o) {
		t.Fatal("sold-out item must block")
	}
	o.Items[1].Status = ItemCancelled
	if !CanMarkPurchased(o) {
		t.Fatal("cancelled item must not block")
	}
	o.Items[0].StorePrice = p(219900) // +22% → needs confirmation
	if CanMarkPurchased(o) {
		t.Fatal("unconfirmed price rise must block")
	}
	all := Order{Items: []Item{{Status: ItemCancelled, Qty: 1}}}
	if CanMarkPurchased(all) {
		t.Fatal("order with every item cancelled cannot be purchased")
	}
}

func TestSummarize(t *testing.T) {
	o := Order{Paid: p(777700), Fee: 58000, Items: []Item{
		{Status: ItemChecked, Qty: 1, Price: p(179900)},
		{Status: ItemSoldOut, Qty: 1, Price: p(399900)},
		{Status: ItemChecked, Qty: 1, Price: p(139900), StorePrice: p(144900)},
	}}
	m := Summarize(o)
	if m.ToBuy != 324800 || m.Covered != 5000 || m.Refunds != 0 {
		t.Fatalf("got %+v", m)
	}
	o.Items[1].Status = ItemCancelled
	if m = Summarize(o); m.Refunds != 399900 || m.ToBuy != 324800 {
		t.Fatalf("after cancel got %+v", m)
	}
}

func TestUAH(t *testing.T) {
	for in, want := range map[Money]string{777700: "7 777 ₴", 5000: "50 ₴", 179950: "1 799,50 ₴", 1248000000: "12 480 000 ₴", -399900: "−3 999 ₴"} {
		if got := UAH(in); got != want {
			t.Errorf("UAH(%d) = %q, want %q", in, got, want)
		}
	}
}
