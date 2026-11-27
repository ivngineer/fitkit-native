package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"fitkit/server/internal/workspace/httpapi"
	"fitkit/server/internal/workspace/store"
)

type adminClient struct {
	t     *testing.T
	base  string
	token string
}

func (a *adminClient) do(method, path string, body any) res {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return res{resp.StatusCode, b}
}

// adminFor serves the admin panel API next to the workspace env, signed in as "boss".
func adminFor(e *env, admins map[string]string) *adminClient {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpapi.NewAdmin(httpapi.AdminConfig{Service: e.svc, Log: log, Admins: admins}))
	e.t.Cleanup(srv.Close)
	return &adminClient{t: e.t, base: srv.URL}
}

func (a *adminClient) signIn(login, password string) {
	a.t.Helper()
	r := a.do("POST", "/admin/api/auth/login", map[string]string{"login": login, "password": password})
	if r.code != 200 {
		a.t.Fatalf("admin login: %d %s", r.code, r.body)
	}
	var out struct{ Token string }
	r.json(&out)
	a.token = out.Token
}

type employeeView struct {
	ID, Name, Email, Role string
	IsTeamLead            bool
	Stats                 struct{ Completed, InProgress, OnTheWay, Cancelled, Complaints int }
}

func (a *adminClient) employee(email string) employeeView {
	a.t.Helper()
	var list []employeeView
	a.do("GET", "/admin/api/employees", nil).json(&list)
	for _, e := range list {
		if e.Email == email {
			return e
		}
	}
	a.t.Fatalf("employee %s not listed", email)
	return employeeView{}
}

func TestAdminSignIn(t *testing.T) {
	e := setup(t)
	none := adminFor(e, nil)
	e.want((none.do("POST", "/admin/api/auth/login", map[string]string{"login": "boss", "password": "x"})), 503, "no admins configured")

	a := adminFor(e, map[string]string{"boss": "boss-password-1", "ivan": "ivan-password-2"})
	e.want(a.do("GET", "/admin/api/employees", nil), 401, "no session")
	e.want(a.do("POST", "/admin/api/auth/login", map[string]string{"login": "boss", "password": "ivan-password-2"}), 401, "someone else's password")
	a.signIn("ivan", "ivan-password-2")
	var me struct{ Login string }
	a.do("GET", "/admin/api/me", nil).json(&me)
	if me.Login != "ivan" {
		t.Fatalf("me: %+v", me)
	}
	// employee sessions don't open the admin panel
	anna := e.login("anna@fitkit.space")
	e.want((&adminClient{t: t, base: a.base, token: anna}).do("GET", "/admin/api/employees", nil), 401, "employee token")
	e.want(a.do("POST", "/admin/api/auth/logout", nil), 204, "logout")
	e.want(a.do("GET", "/admin/api/me", nil), 401, "after logout")

	for i := 0; i < 10; i++ {
		a.do("POST", "/admin/api/auth/login", map[string]string{"login": "boss", "password": "nope"})
	}
	e.want(a.do("POST", "/admin/api/auth/login", map[string]string{"login": "boss", "password": "boss-password-1"}), 429, "throttled")
}

func TestAdminManagesEmployees(t *testing.T) {
	e := setup(t)
	a := adminFor(e, map[string]string{"boss": "boss-password-1"})
	a.signIn("boss", "boss-password-1")

	if iryna := a.employee("iryna@fitkit.space"); !iryna.IsTeamLead || iryna.Role != "buyer" {
		t.Fatalf("seeded team lead: %+v", iryna)
	}
	if anna := a.employee("anna@fitkit.space"); anna.Stats.InProgress != 5 || anna.Stats.OnTheWay != 5 {
		t.Fatalf("anna stats: %+v", anna.Stats)
	}

	// add
	newbie := map[string]any{"name": "Марко Шевчук", "email": "marko@fitkit.space", "role": "buyer", "password": "short"}
	e.want(a.do("POST", "/admin/api/employees", newbie), 400, "short password")
	newbie["password"] = "marko-password-1"
	newbie["email"] = "not-an-email"
	e.want(a.do("POST", "/admin/api/employees", newbie), 400, "bad e-mail")
	newbie["email"] = "anna@fitkit.space"
	e.want(a.do("POST", "/admin/api/employees", newbie), 409, "taken e-mail")
	newbie["email"] = "Marko@Fitkit.space"
	e.want(a.do("POST", "/admin/api/employees", newbie), 201, "create")
	marko := a.employee("marko@fitkit.space")
	tok := e.do("POST", "/api/auth/login", "", map[string]string{"email": "marko@fitkit.space", "password": "marko-password-1"})
	e.want(tok, 200, "new employee signs in")
	var signed struct{ Token string }
	tok.json(&signed)

	// change login, role and team lead
	e.want(a.do("PATCH", "/admin/api/employees/"+marko.ID, map[string]any{"name": "Марко Ш.", "email": "marko.s@fitkit.space", "role": "warehouse", "isTeamLead": true}), 200, "update")
	if m := a.employee("marko.s@fitkit.space"); m.Name != "Марко Ш." || m.Role != "warehouse" || !m.IsTeamLead {
		t.Fatalf("after update: %+v", m)
	}
	e.want(e.do("POST", "/api/auth/login", "", map[string]string{"email": "marko@fitkit.space", "password": "marko-password-1"}), 401, "old login")

	// new password ends existing sessions
	e.want(a.do("PUT", "/admin/api/employees/"+marko.ID+"/password", map[string]string{"password": "123"}), 400, "short new password")
	e.want(a.do("PUT", "/admin/api/employees/"+marko.ID+"/password", map[string]string{"password": "marko-password-2"}), 204, "set password")
	e.want(e.do("GET", "/api/me", signed.Token, nil), 401, "session after password change")
	e.want(e.do("POST", "/api/auth/login", "", map[string]string{"email": "marko.s@fitkit.space", "password": "marko-password-2"}), 200, "new password")

	// detail
	var detail struct {
		employeeView
		Orders []struct{ Number, Status string }
	}
	a.do("GET", "/admin/api/employees/"+a.employee("anna@fitkit.space").ID, nil).json(&detail)
	if detail.Name != "Анна Коваль" || len(detail.Orders) == 0 {
		t.Fatalf("detail: %+v", detail)
	}

	// delete: no access, unfinished orders back to the queue, purchased ones keep her for history
	annaID := a.employee("anna@fitkit.space").ID
	annaTok := e.login("anna@fitkit.space")
	e.want(a.do("DELETE", "/admin/api/employees/"+annaID, nil), 204, "delete")
	e.want(e.do("GET", "/api/me", annaTok, nil), 401, "deleted employee session")
	e.want(e.do("POST", "/api/auth/login", "", map[string]string{"email": "anna@fitkit.space", "password": "fitkit-demo"}), 401, "deleted employee login")
	taras := e.login("taras@fitkit.space")
	if o := e.orderByNumber(taras, "#001"); o.Status != "new" || o.OwnerID != nil {
		t.Fatalf("#001 after delete: %s %v", o.Status, o.OwnerID)
	}
	if o := e.orderByNumber(taras, "#0968"); o.Status != "purchased" || o.OwnerID == nil || *o.OwnerID != annaID {
		t.Fatalf("#0968 after delete: %+v", o)
	}
	e.want(a.do("DELETE", "/admin/api/employees/"+annaID, nil), 404, "delete twice")
	// her e-mail can be used again
	newbie["email"], newbie["password"] = "anna@fitkit.space", "anna-new-password"
	e.want(a.do("POST", "/admin/api/employees", newbie), 201, "reuse e-mail")
}

func TestTeamLeadTransfers(t *testing.T) {
	e := setup(t)
	anna, iryna, taras := e.login("anna@fitkit.space"), e.login("iryna@fitkit.space"), e.login("taras@fitkit.space")
	var tarasMe struct{ ID string }
	e.do("GET", "/api/me", taras, nil).json(&tarasMe)
	o := e.orderByNumber(anna, "#001")

	e.want(e.do("POST", "/api/orders/"+o.ID+"/assign", anna, map[string]string{"employeeId": tarasMe.ID}), 403, "not a team lead")
	// a team lead hands orders over but doesn't edit someone else's
	e.want(e.do("PATCH", "/api/orders/"+o.ID+"/items/"+o.Items[1].ID, iryna, map[string]string{"action": "cancel"}), 403, "team lead edits")
	e.want(e.do("POST", "/api/orders/"+o.ID+"/assign", iryna, map[string]string{"employeeId": "nobody"}), 400, "unknown employee")
	r := e.do("POST", "/api/orders/"+o.ID+"/assign", iryna, map[string]string{"employeeId": tarasMe.ID})
	e.want(r, 200, "transfer")
	r.json(&o)
	if o.OwnerID == nil || *o.OwnerID != tarasMe.ID || o.History[len(o.History)-1].What != "Передано: Тарас Мельник" {
		t.Fatalf("after transfer: %+v", o)
	}
	e.want(e.do("PATCH", "/api/orders/"+o.ID+"/items/"+o.Items[1].ID, anna, map[string]string{"action": "cancel"}), 403, "previous owner")
	e.want(e.do("PATCH", "/api/orders/"+o.ID+"/items/"+o.Items[1].ID, taras, map[string]string{"action": "cancel"}), 200, "new owner")

	// a new request handed over is taken in the name of the new owner
	req := e.orderByNumber(anna, "#003")
	e.do("POST", "/api/orders/"+req.ID+"/assign", iryna, map[string]string{"employeeId": tarasMe.ID}).json(&req)
	if req.Status != "in_progress" || *req.OwnerID != tarasMe.ID {
		t.Fatalf("new request after transfer: %+v", req)
	}
}

func TestAdminAnalytics(t *testing.T) {
	e := setup(t)
	a := adminFor(e, map[string]string{"boss": "boss-password-1"})
	a.signIn("boss", "boss-password-1")
	anna, taras := e.login("anna@fitkit.space"), e.login("taras@fitkit.space")
	// Anna's two shipped orders reach the client.
	for _, n := range []string{"#0918", "#0932"} {
		e.want(e.do("POST", "/api/orders/"+e.orderByNumber(anna, n).ID+"/status", anna, map[string]string{"status": "delivered"}), 200, "deliver "+n)
	}
	// Two complaints about Taras's orders, one about Anna's.
	for _, n := range []string{"#0990", "#0987"} {
		if _, err := e.svc.Complain(context.Background(), e.orderByNumber(taras, n).ID, "Довго немає відповіді"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Complain(context.Background(), e.orderByNumber(anna, "#001").ID, "Не той розмір"); err != nil {
		t.Fatal(err)
	}

	var out struct {
		Totals      struct{ Employees, Orders, Delivered, InProgress, Complaints int }
		Leaderboard []struct {
			Employee struct{ Name string }
			Count    int
		}
		MostComplaints []struct {
			Employee struct{ Name string }
			Count    int
		}
	}
	r := a.do("GET", "/admin/api/analytics", nil)
	e.want(r, 200, "analytics")
	r.json(&out)
	if out.Totals.Employees != 4 || out.Totals.Delivered != 2 || out.Totals.Complaints != 3 || out.Totals.Orders != 21 {
		t.Fatalf("totals: %+v", out.Totals)
	}
	if len(out.Leaderboard) != 4 || out.Leaderboard[0].Employee.Name != "Анна Коваль" || out.Leaderboard[0].Count != 2 {
		t.Fatalf("leaderboard: %+v", out.Leaderboard)
	}
	if len(out.MostComplaints) != 2 || out.MostComplaints[0].Employee.Name != "Тарас Мельник" || out.MostComplaints[0].Count != 2 {
		t.Fatalf("complaints: %+v", out.MostComplaints)
	}
	var detail struct {
		Complaints []struct{ OrderNumber, Text string }
	}
	a.do("GET", "/admin/api/employees/"+a.employee("taras@fitkit.space").ID, nil).json(&detail)
	if len(detail.Complaints) != 2 || detail.Complaints[0].Text != "Довго немає відповіді" {
		t.Fatalf("taras complaints: %+v", detail.Complaints)
	}
}

func TestRoleMigration(t *testing.T) {
	e := setup(t)
	// An employee saved with the old "lead" role becomes a buyer and team lead.
	if _, err := e.st.DB.Exec(`UPDATE employees SET role = 'lead', is_team_lead = 0 WHERE email = 'taras@fitkit.space'`); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	emp, err := e.st.Q().EmployeeByEmail(context.Background(), "taras@fitkit.space")
	if err != nil || emp.Role != "buyer" || !emp.IsTeamLead {
		t.Fatalf("migrated: %+v %v", emp, err)
	}
	_ = store.ErrNotFound
}
