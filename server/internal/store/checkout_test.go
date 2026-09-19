package store_test

import (
	"context"
	"testing"
	"time"

	"fitkit/server/internal/store"
	"fitkit/server/internal/testutil"
)

func testUser(t *testing.T, st *store.Store) store.User {
	t.Helper()
	u, err := st.CreateUser(context.Background(), "u-"+store.NewID()+"@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func testPin(t *testing.T, st *store.Store) store.Pin {
	t.Helper()
	p := store.Pin{
		PinterestID:    store.NewID(),
		Title:          "test pin",
		ImageSourceURL: "https://example.com/x.jpg",
		ImageFile:      "x.jpg",
		Width:          100,
		Height:         100,
	}
	if err := st.InsertPin(context.Background(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAddressDefaults(t *testing.T) {
	ctx := context.Background()
	st := testutil.OpenStore(t)
	u := testUser(t, st)

	a1 := &store.Address{UserID: u.ID, Kind: "shipping", Country: "US", FinalCountry: "US"}
	if err := st.CreateAddress(ctx, a1); err != nil {
		t.Fatal(err)
	}
	if !a1.IsDefault {
		t.Fatal("first address should become default")
	}

	a2 := &store.Address{UserID: u.ID, Kind: "shipping", Country: "US", FinalCountry: "US", IsDefault: true}
	if err := st.CreateAddress(ctx, a2); err != nil {
		t.Fatal(err)
	}
	if !a2.IsDefault {
		t.Fatal("a2 should be default")
	}
	got1, err := st.AddressByID(ctx, u.ID, a1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got1.IsDefault {
		t.Fatal("a1 should have lost default")
	}

	// Deleting the default promotes the oldest remaining address.
	if err := st.DeleteAddress(ctx, u.ID, a2.ID); err != nil {
		t.Fatal(err)
	}
	got1, err = st.AddressByID(ctx, u.ID, a1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got1.IsDefault {
		t.Fatal("a1 should be promoted to default")
	}
}

func TestUpdateAddressCrossUserAndDefaultKeep(t *testing.T) {
	ctx := context.Background()
	st := testutil.OpenStore(t)
	u1 := testUser(t, st)
	u2 := testUser(t, st)

	a := &store.Address{UserID: u1.ID, Kind: "shipping", Country: "US", FinalCountry: "US"}
	if err := st.CreateAddress(ctx, a); err != nil {
		t.Fatal(err)
	}

	// Update from another user is not found.
	wrong := *a
	wrong.UserID = u2.ID
	wrong.Label = "hacked"
	if err := st.UpdateAddress(ctx, &wrong); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	// Un-defaulting the sole default keeps it default.
	upd := *a
	upd.IsDefault = false
	upd.Label = "home"
	if err := st.UpdateAddress(ctx, &upd); err != nil {
		t.Fatal(err)
	}
	if !upd.IsDefault {
		t.Fatal("only address must stay default")
	}
	got, err := st.AddressByID(ctx, u1.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsDefault || got.Label != "home" {
		t.Fatalf("got %+v", got)
	}

	// AddressByID cross-user is not found.
	if _, err := st.AddressByID(ctx, u2.ID, a.ID); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSetSizesReplace(t *testing.T) {
	ctx := context.Background()
	st := testutil.OpenStore(t)
	u := testUser(t, st)

	if err := st.SetSizes(ctx, u.ID, map[string]string{"tops": "M", "shoes": "10"}); err != nil {
		t.Fatal(err)
	}
	sizes, err := st.Sizes(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes["tops"] != "M" || sizes["shoes"] != "10" {
		t.Fatalf("got %+v", sizes)
	}

	// Replace wholesale; empty entries skipped.
	if err := st.SetSizes(ctx, u.ID, map[string]string{"tops": "L", "hats": ""}); err != nil {
		t.Fatal(err)
	}
	sizes, err = st.Sizes(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 1 || sizes["tops"] != "L" {
		t.Fatalf("got %+v", sizes)
	}

	u2 := testUser(t, st)
	sizes2, err := st.Sizes(ctx, u2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sizes2 == nil || len(sizes2) != 0 {
		t.Fatalf("want empty non-nil map, got %+v", sizes2)
	}
}

func TestCreateLookAndLookByID(t *testing.T) {
	ctx := context.Background()
	st := testutil.OpenStore(t)
	u := testUser(t, st)
	p := testPin(t, st)

	l := &store.Look{
		UserID:   u.ID,
		PinID:    p.ID,
		PlanJSON: `{"total":100}`,
		Stores: []store.LookStore{
			{Merchant: "zara", Status: "pending"},
			{Merchant: "hm", Status: "pending"},
		},
	}
	if err := st.CreateLook(ctx, l); err != nil {
		t.Fatal(err)
	}
	if l.ID == "" {
		t.Fatal("want ID set")
	}
	if l.Stores[0].Position != 0 || l.Stores[1].Position != 1 {
		t.Fatalf("want positions set in order, got %+v", l.Stores)
	}

	got, err := st.LookByID(ctx, u.ID, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Stores) != 2 || got.Stores[0].Merchant != "zara" || got.Stores[1].Merchant != "hm" {
		t.Fatalf("got %+v", got.Stores)
	}

	// Cross-user lookup not found.
	u2 := testUser(t, st)
	if _, err := st.LookByID(ctx, u2.ID, l.ID); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLooksNewestFirst(t *testing.T) {
	ctx := context.Background()
	st := testutil.OpenStore(t)
	u := testUser(t, st)
	p := testPin(t, st)

	var ids []string
	for i := 0; i < 3; i++ {
		l := &store.Look{UserID: u.ID, PinID: p.ID, PlanJSON: "{}"}
		if err := st.CreateLook(ctx, l); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, l.ID)
		time.Sleep(2 * time.Millisecond)
	}

	looks, err := st.Looks(ctx, u.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(looks) != 3 {
		t.Fatalf("want 3 looks, got %d", len(looks))
	}
	if looks[0].ID != ids[2] || looks[1].ID != ids[1] || looks[2].ID != ids[0] {
		t.Fatalf("want newest first, got %+v", looks)
	}
}

func TestUpdateLookStore(t *testing.T) {
	ctx := context.Background()
	st := testutil.OpenStore(t)
	u := testUser(t, st)
	u2 := testUser(t, st)
	p := testPin(t, st)

	l := &store.Look{UserID: u.ID, PinID: p.ID, PlanJSON: "{}", Stores: []store.LookStore{
		{Merchant: "zara", Status: "pending", OrderRef: "orig-ref", Carrier: "orig-carrier"},
	}}
	if err := st.CreateLook(ctx, l); err != nil {
		t.Fatal(err)
	}

	// Wrong owner: not found.
	if _, err := st.UpdateLookStore(ctx, u2.ID, l.ID, "zara", store.LookStoreUpdate{Status: "ordered"}); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	// Unknown merchant: not found.
	if _, err := st.UpdateLookStore(ctx, u.ID, l.ID, "asos", store.LookStoreUpdate{Status: "ordered"}); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	tracking := "TRACK123"
	got, err := st.UpdateLookStore(ctx, u.ID, l.ID, "zara", store.LookStoreUpdate{Status: "ordered", TrackingNo: &tracking})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ordered" || got.TrackingNo != "TRACK123" {
		t.Fatalf("got %+v", got)
	}
	// nil pointer fields (OrderRef, Carrier) must be left unchanged.
	if got.OrderRef != "orig-ref" || got.Carrier != "orig-carrier" {
		t.Fatalf("want unchanged fields kept, got %+v", got)
	}
}

func TestShopCache(t *testing.T) {
	ctx := context.Background()
	st := testutil.OpenStore(t)

	// Miss.
	_, ok, err := st.CachedShopData(ctx, "https://shop.example/product", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("want miss on empty cache")
	}

	if err := st.PutShopData(ctx, "https://shop.example/product", `{"price":1}`); err != nil {
		t.Fatal(err)
	}

	body, ok, err := st.CachedShopData(ctx, "https://shop.example/product", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || body != `{"price":1}` {
		t.Fatalf("want hit with body, got ok=%v body=%q", ok, body)
	}

	// Negative maxAge forces expiry.
	_, ok, err = st.CachedShopData(ctx, "https://shop.example/product", -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("want expired entry to miss")
	}
}
