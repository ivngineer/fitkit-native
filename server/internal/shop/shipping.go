package shop

import (
	_ "embed"
	"encoding/json"
	"slices"
	"strings"

	"fitkit/server/internal/store"
)

// merchants.json is maintained by hand for the stores that show up most in
// listings: where each one ships and where it ships from.
//
//go:embed merchants.json
var merchantsJSON []byte

type merchantInfo struct {
	// ShipsTo lists ISO country codes; "EU" stands for every EU member and
	// "*" for anywhere.
	ShipsTo []string `json:"shipsTo"`
	// Origin is the region orders ship from: "US", "EU", "GB" or "CN".
	Origin string `json:"origin"`
}

var merchantTable = func() map[string]merchantInfo {
	var m map[string]merchantInfo
	if err := json.Unmarshal(merchantsJSON, &m); err != nil {
		panic("shop: bad merchants.json: " + err.Error())
	}
	return m
}()

var euCountries = []string{
	"AT", "BE", "BG", "HR", "CY", "CZ", "DK", "EE", "FI", "FR", "DE", "GR", "HU", "IE",
	"IT", "LV", "LT", "LU", "MT", "NL", "PL", "PT", "RO", "SK", "SI", "ES", "SE",
}

func InEU(country string) bool { return slices.Contains(euCountries, strings.ToUpper(country)) }

// shipping is what's known about getting one store's order to a country.
type shipping int

const (
	shipsUnknown shipping = iota
	shipsYes
	shipsNo
)

// shipsTo applies the rules in order: the hand-kept merchant table, the
// store's own list (Shopify), otherwise unknown.
func shipsTo(merchant string, sf *Storefront, country string) shipping {
	if info, ok := merchantTable[merchant]; ok && len(info.ShipsTo) > 0 {
		for _, c := range info.ShipsTo {
			if c == "*" || c == country || (c == "EU" && InEU(country)) {
				return shipsYes
			}
		}
		return shipsNo
	}
	if sf != nil {
		if ships, known := sf.Ships(country); known {
			if ships {
				return shipsYes
			}
			return shipsNo
		}
	}
	return shipsUnknown
}

// origin guesses the region a store ships from: the merchant table, then the
// store's currency, then its domain.
func origin(merchant string, sf *Storefront) string {
	if info, ok := merchantTable[merchant]; ok && info.Origin != "" {
		return info.Origin
	}
	if sf != nil {
		switch sf.Currency {
		case "EUR", "PLN", "CZK", "SEK", "DKK", "HUF", "RON", "BGN":
			return "EU"
		case "GBP":
			return "GB"
		case "USD":
			return "US"
		}
	}
	tld := merchant[strings.LastIndex(merchant, ".")+1:]
	switch {
	case strings.HasSuffix(merchant, ".co.uk") || tld == "uk":
		return "GB"
	case InEU(tld) || tld == "eu":
		return "EU"
	}
	return "US"
}

// warehouseFor is the forwarder warehouse country that suits a store: US
// stores ship to the US warehouse, everything else to Poland.
func warehouseFor(origin string) string {
	if origin == "US" {
		return "US"
	}
	return "PL"
}

var countryNames = map[string]string{"US": "the US", "PL": "Poland", "UA": "Ukraine", "GB": "the UK", "DE": "Germany"}

func countryName(code string) string {
	if n, ok := countryNames[code]; ok {
		return n
	}
	return code
}

var forwarderNames = map[string]string{"np_shopping": "NP Shopping", "meest": "Meest", "ukraine_express": "Ukraine Express"}

// route is the address one store's order goes to.
type route struct {
	address  *store.Address
	via      string // "direct" or "forwarder"
	note     string
	warnings []string
}

// chooseRoute picks the address for one store. dest is what the user picked
// under "Deliver to"; book is their whole address book, which holds any
// forwarder addresses.
func chooseRoute(merchant string, sf *Storefront, dest *store.Address, book []store.Address) route {
	if dest == nil {
		return route{}
	}
	if dest.Kind == KindForwarder {
		return forwarderRoute(dest, dest.FinalCountry)
	}
	country := dest.Country
	ships := shipsTo(merchant, sf, country)
	if country == "UA" && ships != shipsYes {
		if fwd := pickForwarder(book, warehouseFor(origin(merchant, sf))); fwd != nil {
			return forwarderRoute(fwd, "UA")
		}
		r := route{address: dest, via: "direct"}
		if ships == shipsNo {
			r.warnings = append(r.warnings, "This store doesn't ship to Ukraine. Add your NP Shopping address in Settings to route it through a forwarder.")
		} else {
			r.warnings = append(r.warnings, "This store may not ship to Ukraine. Add your NP Shopping address in Settings to route it through a forwarder.")
		}
		return r
	}
	r := route{address: dest, via: "direct"}
	if ships == shipsNo {
		r.warnings = append(r.warnings, "This store doesn't ship to "+countryName(country)+". Pick another listing for these pieces.")
	}
	return r
}

func forwarderRoute(fwd *store.Address, finalCountry string) route {
	name := forwarderNames[fwd.Forwarder]
	if name == "" {
		name = "forwarding"
	}
	r := route{address: fwd, via: "forwarder",
		note: "Ships to your " + name + " address in " + countryName(fwd.Country) + ", then on to " + countryName(finalCountry) + "."}
	if fwd.Forwarder == "np_shopping" && finalCountry == "UA" {
		r.note = "Ships to your NP Shopping address in " + countryName(fwd.Country) +
			", then about 5–10 days to your branch. Nova Poshta delivery fee paid on pickup, from $3 per 0.5 kg."
	}
	return r
}

// pickForwarder prefers a forwarder address in the given warehouse country,
// then the default one, then any.
func pickForwarder(book []store.Address, warehouse string) *store.Address {
	var fallback *store.Address
	for i := range book {
		a := &book[i]
		if a.Kind != KindForwarder {
			continue
		}
		if a.Country == warehouse {
			return a
		}
		if fallback == nil || a.IsDefault {
			fallback = a
		}
	}
	return fallback
}

// customsWarnings flags duties the buyer should expect. subtotal is in
// minor units of currency.
func customsWarnings(merchant string, sf *Storefront, finalCountry string, subtotal int64, currency string) []string {
	var out []string
	from := origin(merchant, sf)
	if InEU(finalCountry) && from != "EU" {
		out = append(out, "Ships from outside the EU: expect about €3 duty per item plus VAT on delivery.")
	}
	if finalCountry == "UA" && subtotal > 150_00 && (currency == "EUR" || currency == "USD" || currency == "GBP") {
		out = append(out, "Over €150: Ukraine charges 10% duty and 20% VAT on the amount above €150.")
	}
	return out
}

// Address kinds.
const (
	KindHome      = "home"
	KindNPBranch  = "np_branch"
	KindForwarder = "forwarder"
)

var Forwarders = []string{"np_shopping", "meest", "ukraine_express"}

// prefill turns an address into Shopify checkout fields.
func prefill(email string, a *store.Address) Prefill {
	p := Prefill{Email: email}
	if a == nil {
		return p
	}
	f := a.Fields
	p.FirstName, p.LastName = f.FirstName, f.LastName
	p.Address1, p.Address2 = f.Line1, f.Line2
	p.City, p.Province, p.Zip, p.Phone = f.City, f.Region, f.Zip, f.Phone
	p.Country = a.Country
	switch a.Kind {
	case KindForwarder:
		if f.SuiteID != "" {
			p.Address2 = strings.TrimSpace(p.Address2 + " " + f.SuiteID)
		}
	case KindNPBranch:
		if p.Address1 == "" {
			p.Address1 = "Nova Poshta branch " + f.NPBranch
		}
	}
	return p
}

// AddressSummary is a one-line address for display and copying.
func AddressSummary(a *store.Address) string {
	f := a.Fields
	var parts []string
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" {
			parts = append(parts, v)
		}
	}
	add(strings.TrimSpace(f.FirstName + " " + f.LastName))
	if a.Kind == KindNPBranch && f.NPBranch != "" {
		add("Nova Poshta branch " + f.NPBranch)
	}
	add(f.Line1)
	add(f.Line2)
	if a.Kind == KindForwarder {
		add(f.SuiteID)
	}
	add(strings.TrimSpace(f.City + " " + f.Zip))
	add(f.Region)
	add(a.Country)
	add(f.Phone)
	return strings.Join(parts, ", ")
}
