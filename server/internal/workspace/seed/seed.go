// Package seed loads the demo data used by the front-end mock (same people, orders, parcels).
package seed

import (
	"context"
	"time"

	"golang.org/x/crypto/bcrypt"

	"fitkit/server/internal/workspace/domain"
	"fitkit/server/internal/workspace/store"
)

// DemoPassword is the password of every demo employee.
const DemoPassword = "fitkit-demo"

func p[T any](v T) *T { return &v }

func uah(v int64) *domain.Money { m := v * 100; return &m }

var storeURL = map[string]string{
	"Zara": "https://www.zara.com/ua/", "H&M": "https://www2.hm.com/", "Answear": "https://answear.ua/",
	"Reserved": "https://www.reserved.com/ua/", "ASOS": "https://www.asos.com/", "Nike": "https://www.nike.com/",
	"Mango": "https://shop.mango.com/ua/", "COS": "https://www.cos.com/", "Massimo Dutti": "https://www.massimodutti.com/ua/",
	"AliExpress": "https://www.aliexpress.com/",
}

func item(photo, name, store, size string, price *domain.Money, st domain.ItemStatus, mods ...func(*domain.Item)) domain.Item {
	it := domain.Item{Photo: "/assets/photos/" + photo + ".jpg", Name: name, Store: store, URL: storeURL[store], Size: size, Qty: 1, Price: price, Status: st}
	for _, m := range mods {
		m(&it)
	}
	return it
}
func comment(c string) func(*domain.Item)    { return func(i *domain.Item) { i.Comment = &c } }
func storePrice(v int64) func(*domain.Item)  { return func(i *domain.Item) { i.StorePrice = uah(v) } }
func sentAgo(t time.Time) func(*domain.Item) { return func(i *domain.Item) { i.ConfirmSentAt = &t } }

// Load wipes the database and inserts the demo data. Times are relative to now.
func Load(ctx context.Context, st *store.Store) error {
	now := time.Now()
	ago := func(min int) time.Time { return now.Add(-time.Duration(min) * time.Minute) }
	at := func(min int) time.Time { return now.Add(time.Duration(min) * time.Minute) }

	if err := st.Reset(ctx); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(DemoPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return st.Tx(ctx, func(q *store.Q) error {
		emp := map[string]string{}
		for _, e := range []struct {
			key, name, email string
			role             domain.Role
		}{
			{"anna", "Анна Коваль", "anna@fitkit.space", domain.RoleBuyer},
			{"iryna", "Ірина Руденко", "iryna@fitkit.space", domain.RoleLead},
			{"taras", "Тарас Мельник", "taras@fitkit.space", domain.RoleBuyer},
			{"oleh", "Олег Савчук", "sklad@fitkit.space", domain.RoleWarehouse},
		} {
			x, err := q.CreateEmployee(ctx, e.name, e.email, e.role, string(hash))
			if err != nil {
				return err
			}
			emp[e.key] = x.ID
		}

		cl := map[string]string{}
		for _, c := range []struct {
			key string
			c   domain.Client
		}{
			{"maria", domain.Client{Name: "Марія Лебеденко", Telegram: p("@maria_l"), Phone: "+380 67 214 45 12", City: "Київ", Branch: "Відділення №48, вул. Велика Васильківська, 72", Sizes: "S–M · W28 L32 · 38", Stores: "Zara, COS, Massimo Dutti", Dislikes: p("синтетика, яскраві принти"), Note: p("Zara на неї маломірить — беріть на розмір більше.")}},
			{"ihor", domain.Client{Name: "Ігор Соколенко", Telegram: p("@ihor_s"), Phone: "+380 50 318 11 07", City: "Львів", Branch: "Відділення №14, вул. Городоцька, 120", Sizes: "L · 44", Stores: "Answear, Nike"}},
			{"olha", domain.Client{Name: "Ольга Петренко", Phone: "+380 93 402 80 21", City: "Одеса", Branch: "Відділення №3, вул. Дерибасівська, 5", Sizes: "XS–S · W26 · 37", Stores: "Reserved, Zara"}},
			{"daria", domain.Client{Name: "Дарія Вовк", Telegram: p("@dariavovk"), Phone: "+380 63 775 52 90", City: "Київ", Branch: "Відділення №101, просп. Перемоги, 37", Sizes: "M · W29", Stores: "ASOS, H&M"}},
			{"pavlo", domain.Client{Name: "Павло Тихоненко", Phone: "+380 97 561 33 18", City: "Дніпро", Branch: "Відділення №12, просп. Яворницького, 40", Sizes: "L · 43", Stores: "Nike, Zara"}},
			{"kseniia", domain.Client{Name: "Ксенія Орлик", Telegram: p("@ks_orlyk"), Phone: "+380 66 129 74 05", City: "Харків", Branch: "Відділення №7, вул. Сумська, 18", Sizes: "S · 38", Stores: "Mango, COS"}},
			{"artem", domain.Client{Name: "Артем Білик", Phone: "+380 68 233 19 44", City: "Київ", Branch: "Поштомат №31045, вул. Хрещатик, 3", Sizes: "M · 42", Stores: "Zara, AliExpress"}},
			{"olena", domain.Client{Name: "Олена Смирнова", Phone: "+380 99 640 62 31", City: "Вінниця", Branch: "Відділення №2, вул. Соборна, 54", Sizes: "S · W27 · 38", Stores: "Massimo Dutti, H&M"}},
		} {
			id, err := q.UpsertClient(ctx, "demo-"+c.key, c.c)
			if err != nil {
				return err
			}
			cl[c.key] = id
		}

		type ord struct {
			key, client, owner string
			status             domain.OrderStatus
			created, due       time.Time
			paid               *domain.Money
			fee                int64
			problem, location  string
			items              []domain.Item
			history            []domain.HistoryEvent
			notes              []domain.Note
		}
		orders := []ord{
			{key: "009", client: "olena", status: domain.StatusNew, created: ago(102), due: ago(42), paid: uah(3999), items: []domain.Item{item("jacket-black", "Чорна куртка", "Zara", "S", uah(3999), domain.ItemPending, comment("«Як на фото, чорна»"))}},
			{key: "002", client: "ihor", status: domain.StatusNew, created: ago(75), due: ago(15), paid: uah(2499), items: []domain.Item{item("hoodie", "Худі вільного крою", "Answear", "L", uah(2499), domain.ItemPending)}},
			{key: "003", client: "olha", status: domain.StatusNew, created: ago(22), due: at(8), paid: uah(3170), items: []domain.Item{item("sweater-grey", "Сірий в’язаний светр", "Reserved", "S", uah(1771), domain.ItemPending), item("jeans", "Прямі джинси", "Reserved", "W26 L32", uah(1399), domain.ItemPending)}},
			{key: "004", client: "daria", status: domain.StatusNew, created: ago(1), due: at(29), items: []domain.Item{item("hoodie", "Базове худі", "ASOS", "M", nil, domain.ItemPending), item("sweatpants", "Джогери", "ASOS", "M", nil, domain.ItemPending), item("scarf", "Вовняний шарф", "ASOS", "Один розмір", nil, domain.ItemPending)}},
			{key: "005", client: "pavlo", status: domain.StatusNew, created: ago(0), due: at(35), paid: uah(5899), items: []domain.Item{item("sneakers", "Кросівки Air", "Nike", "43", uah(5899), domain.ItemPending)}},
			{key: "006", client: "kseniia", status: domain.StatusNew, created: ago(0), due: at(40), paid: uah(4270), items: []domain.Item{item("scarf", "Картатий шарф", "Mango", "Один розмір", uah(1299), domain.ItemPending), item("loafers", "Шкіряні лофери", "COS", "38", uah(2971), domain.ItemPending)}},
			{key: "007", client: "artem", status: domain.StatusNew, created: ago(0), due: at(48), items: []domain.Item{item("stripe", "Смугастий светр", "Zara", "M", nil, domain.ItemPending), item("sweatpants2", "Спортивні штани", "Zara", "M", nil, domain.ItemPending), item("loafers", "Лофери", "Zara", "42", nil, domain.ItemPending)}},

			{key: "001", client: "maria", owner: "anna", status: domain.StatusInProgress, created: ago(170), due: at(80), paid: uah(7777), fee: 580,
				problem: "Чорна куртка: немає в наявності — застосуйте правило",
				items: []domain.Item{
					item("stripe", "Смугастий светр", "Zara", "L", uah(1799), domain.ItemChecked, comment("«На розмір більше, люблю вільну посадку»")),
					item("jacket-black", "Чорна куртка", "Zara", "M", uah(3999), domain.ItemSoldOut, comment("«Потрібно до 25 листопада»")),
					item("jeans-black", "Широкі джинси", "H&M", "W28 L32", uah(1399), domain.ItemChecked, comment("«Темні, як на фото»"), storePrice(1449)),
				},
				notes:   []domain.Note{{Author: "Ірина Р.", At: ago(30), Text: "У Massimo Dutti є схожа куртка в M — перевір перед поверненням."}},
				history: []domain.HistoryEvent{{At: ago(150), Who: "Анна Коваль", What: "Заявку взято в роботу"}, {At: ago(60), Who: "Анна Коваль", What: "Чорна куртка: позначено «немає в наявності»"}}},
			{key: "008", client: "olena", owner: "anna", status: domain.StatusInProgress, created: ago(60), due: at(120), paid: uah(3999), items: []domain.Item{item("jacket-black", "Чорна куртка", "Massimo Dutti", "S", uah(3999), domain.ItemPending)}},
			{key: "0993", client: "artem", owner: "anna", status: domain.StatusInProgress, created: ago(80), due: at(160), paid: uah(5380), items: []domain.Item{item("hoodie", "Худі на блискавці", "Zara", "M", uah(2690), domain.ItemChecked), item("sweatpants", "Джогери", "Zara", "M", uah(2690), domain.ItemPending)}},
			{key: "0990", client: "kseniia", owner: "taras", status: domain.StatusInProgress, created: ago(90), due: at(180), paid: uah(1599), items: []domain.Item{item("scarf", "Вовняний шарф", "COS", "Один розмір", uah(1599), domain.ItemPending)}},
			{key: "0987", client: "olha", owner: "taras", status: domain.StatusInProgress, created: ago(100), due: at(210), paid: uah(12480), items: []domain.Item{item("sweater-grey", "Кашеміровий светр", "COS", "S", uah(6490), domain.ItemPending), item("jeans", "Прямі джинси", "COS", "W26", uah(3490), domain.ItemPending), item("loafers", "Лофери", "COS", "37", uah(2500), domain.ItemPending)}},

			{key: "0995", client: "ihor", owner: "anna", status: domain.StatusAwaiting, created: ago(600), due: at(360), paid: uah(5899), items: []domain.Item{item("sneakers", "Кросівки Air", "Nike", "44", uah(5899), domain.ItemAwaiting, storePrice(6599), sentAgo(ago(18*60)))}},
			{key: "0981", client: "pavlo", owner: "taras", status: domain.StatusAwaiting, created: ago(1800), due: ago(120), paid: uah(4480), problem: "Немає відповіді 26 год — потрібне автоповернення", items: []domain.Item{item("stripe", "Смугастий светр", "Zara", "L", uah(2240), domain.ItemChecked), item("sweatpants2", "Спортивні штани", "Zara", "L", uah(2240), domain.ItemAwaiting, storePrice(2590), sentAgo(ago(26*60)))}},
			{key: "0976", client: "daria", owner: "anna", status: domain.StatusAwaiting, created: ago(300), due: at(120), paid: uah(1899), items: []domain.Item{item("jeans-black", "Широкі джинси", "H&M", "W29", uah(1899), domain.ItemAwaiting, storePrice(2069), sentAgo(ago(4*60)))}},

			{key: "0968", client: "olena", owner: "anna", status: domain.StatusPurchased, created: ago(9000), due: at(600), paid: uah(4590), location: "На складі · Нова Пошта", items: []domain.Item{item("loafers", "Шкіряні лофери", "Massimo Dutti", "38", uah(4590), domain.ItemBought)}},
			{key: "0972", client: "artem", owner: "anna", status: domain.StatusPurchased, created: ago(7000), due: at(5000), paid: uah(2140), location: "В дорозі · Cainiao", items: []domain.Item{item("hoodie", "Худі оверсайз", "AliExpress", "L", uah(1290), domain.ItemBought), item("sweatpants", "Джогери карго", "AliExpress", "L", uah(850), domain.ItemBought)}},
			{key: "0951", client: "pavlo", owner: "taras", status: domain.StatusPurchased, created: ago(8000), due: at(300), paid: uah(5899), location: "Чекаємо ТТН · Nike", problem: "Немає ТТН 3 дні", items: []domain.Item{item("sneakers", "Кросівки Air", "Nike", "43", uah(5899), domain.ItemBought)}},
			{key: "0947", client: "kseniia", owner: "anna", status: domain.StatusPurchased, created: ago(11000), due: at(200), paid: uah(5080), location: "В дорозі · DHL", problem: "Немає руху 5 днів", items: []domain.Item{item("sweater-grey", "В’язаний светр", "COS", "S", uah(3490), domain.ItemBought), item("scarf", "Шарф", "COS", "Один розмір", uah(1590), domain.ItemBought)}},

			{key: "0932", client: "olha", owner: "anna", status: domain.StatusShipped, created: ago(12000), due: at(1440), paid: uah(3198), location: "Нова Пошта · прибуде завтра", items: []domain.Item{item("jeans", "Прямі джинси", "Reserved", "W26", uah(1399), domain.ItemBought), item("stripe", "Смугастий топ", "Reserved", "S", uah(1799), domain.ItemBought)}},
			{key: "0918", client: "maria", owner: "anna", status: domain.StatusShipped, created: ago(14000), due: at(300), paid: uah(3999), location: "Кур’єр доставить сьогодні", items: []domain.Item{item("jacket-black", "Вовняне пальто", "Massimo Dutti", "S", uah(3999), domain.ItemBought)}},
		}
		ids := map[string]string{}
		for _, o := range orders {
			var owner *string
			if o.owner != "" {
				owner = p(emp[o.owner])
			}
			var problem, location *string
			if o.problem != "" {
				problem = p(o.problem)
			}
			if o.location != "" {
				location = p(o.location)
			}
			fee := o.fee * 100
			if fee == 0 && o.paid != nil {
				fee = (*o.paid * 75 / 1000) / 1000 * 1000
			}
			id, _, err := q.InsertOrder(ctx, store.NewOrder{Number: "#" + o.key, ExternalID: "demo-" + o.key, ClientID: cl[o.client], OwnerID: owner, Status: o.status, Paid: o.paid, Fee: fee,
				Problem: problem, Location: location, CreatedAt: o.created, DueAt: o.due, Items: o.items})
			if err != nil {
				return err
			}
			ids[o.key] = id
			if err := q.AddHistoryAt(ctx, id, "Система", "Заявка надійшла", o.created); err != nil {
				return err
			}
			for _, h := range o.history {
				if err := q.AddHistoryAt(ctx, id, h.Who, h.What, h.At); err != nil {
					return err
				}
			}
			for _, n := range o.notes {
				if _, err := q.AddNote(ctx, id, p(emp["iryna"]), n.Author, n.Text, n.At); err != nil {
					return err
				}
			}
		}

		type ev struct {
			min           int
			title, detail string
		}
		ships := []struct {
			order, store, carrier, tracking, source, problem string
			leg                                              domain.Leg
			atWarehouse                                      bool
			events                                           []ev
		}{
			{order: "0968", leg: domain.LegIn, store: "Massimo Dutti", carrier: "Нова Пошта", tracking: "2045 0088 1244 71", source: "email", atWarehouse: true, events: []ev{{8600, "Викуплено в Massimo Dutti", ""}, {7200, "Передано Новій Пошті", "ТТН з листа магазину"}, {48, "Прибуло на склад", "перевірено, 3 фото"}}},
			{order: "0972", leg: domain.LegIn, store: "AliExpress", carrier: "Cainiao", tracking: "LP00 4821 7730 15UA", source: "email", events: []ev{{6900, "Викуплено на AliExpress", ""}, {5000, "Прийнято Cainiao", "ТТН з листа магазину"}, {210, "Виїхало із сортувального центру, Шеньчжень", ""}}},
			{order: "0951", leg: domain.LegIn, store: "Nike", problem: "Немає ТТН 3 дні", events: []ev{{7900, "Викуплено в Nike", ""}}},
			{order: "0947", leg: domain.LegIn, store: "COS", carrier: "DHL", tracking: "4821 0093 77", source: "manual", problem: "Немає руху 5 днів", events: []ev{{10800, "Викуплено в COS", ""}, {7300, "Забрано DHL", "ТТН введено закупником"}}},
			{order: "0932", leg: domain.LegOut, carrier: "Нова Пошта", tracking: "2045 0099 8211 67", source: "api", events: []ev{{1500, "ТТН створено", "API Нової Пошти"}, {1300, "Прямує до клієнта", ""}}},
			{order: "0918", leg: domain.LegOut, carrier: "Нова Пошта", tracking: "2045 0055 3000 81", source: "api", events: []ev{{2900, "ТТН створено", "API Нової Пошти"}, {25, "Кур’єр доставить сьогодні", ""}}},
		}
		opt := func(s string) *string {
			if s == "" {
				return nil
			}
			return &s
		}
		for _, s := range ships {
			created := ago(s.events[0].min)
			id, err := q.InsertShipment(ctx, domain.Shipment{OrderID: ids[s.order], Leg: s.leg, Store: opt(s.store), Carrier: opt(s.carrier), Tracking: opt(s.tracking), Source: opt(s.source), Problem: opt(s.problem), AtWarehouse: s.atWarehouse, CreatedAt: created})
			if err != nil {
				return err
			}
			for _, e := range s.events {
				if err := q.AddTrackingEvent(ctx, id, domain.TrackingEvent{At: ago(e.min), Title: e.title, Detail: opt(e.detail)}); err != nil {
					return err
				}
			}
		}

		type msg struct {
			from string
			min  int
			text string
		}
		tickets := []struct {
			client, order, status, summary string
			msgs                           []msg
		}{
			{"maria", "001", "open", "Коли прийде моє замовлення?", []msg{{"system", 40, "Товар скасовано автоматично: Чорна куртка · 3 999 ₴ повернуто · клієнта сповіщено"}, {"client", 19, "Привіт! Гроші за куртку повернули, а коли прийде решта замовлення?"}}},
			{"ihor", "0995", "open", "Можна змінити адресу доставки?", []msg{{"client", 40, "Можна змінити адресу доставки? Мені зручніше інше відділення."}}},
			{"olha", "0932", "open", "Трекінг показує завтра, а кур’єр…", []msg{{"client", 48, "Трекінг показує доставку завтра, а кур’єр подзвонив сьогодні — так і має бути?"}}},
			{"daria", "0976", "resolved", "Повернення отримано", []msg{{"client", 1600, "Ви повернули гроші за старе замовлення?"}, {"support", 1500, "Так, повернення надіслали 20 листопада — це займає до 3 банківських днів."}}},
			{"pavlo", "0981", "resolved", "Питання про розмір", []msg{{"client", 3100, "Спортивні штани в L мені підійдуть?"}, {"support", 3000, "Так, L відповідає вашому звичному розміру в Zara."}}},
		}
		for _, t := range tickets {
			oid := ids[t.order]
			last := t.msgs[len(t.msgs)-1].min
			id, err := q.InsertTicket(ctx, domain.Ticket{ClientID: cl[t.client], OrderID: &oid, Status: t.status, Summary: t.summary, UpdatedAt: ago(last)})
			if err != nil {
				return err
			}
			for _, m := range t.msgs {
				var author *string
				if m.from == "support" {
					author = p(emp["iryna"])
				}
				if _, err := q.AddMessage(ctx, id, m.from, author, m.text, ago(m.min)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
