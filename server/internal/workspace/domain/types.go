// Package domain holds the entities and business rules of the employee workspace.
// It has no I/O: the same rules run in the API and in the front-end (web/src/domain).
package domain

import "time"

// Money is an amount in kopiyky (1 ₴ = 100).
type Money = int64

type OrderStatus string

const (
	StatusNew        OrderStatus = "new"
	StatusInProgress OrderStatus = "in_progress"
	StatusAwaiting   OrderStatus = "awaiting"
	StatusPurchased  OrderStatus = "purchased"
	StatusShipped    OrderStatus = "shipped"
	StatusDelivered  OrderStatus = "delivered"
	StatusCancelled  OrderStatus = "cancelled"
)

func (s OrderStatus) Valid() bool {
	switch s {
	case StatusNew, StatusInProgress, StatusAwaiting, StatusPurchased, StatusShipped, StatusDelivered, StatusCancelled:
		return true
	}
	return false
}

type ItemStatus string

const (
	ItemPending   ItemStatus = "pending"
	ItemChecked   ItemStatus = "checked"
	ItemSoldOut   ItemStatus = "sold_out"
	ItemPriceUp   ItemStatus = "price_up"
	ItemAwaiting  ItemStatus = "awaiting"
	ItemBought    ItemStatus = "bought"
	ItemCancelled ItemStatus = "cancelled"
)

type Role string

const (
	RoleBuyer     Role = "buyer"
	RoleWarehouse Role = "warehouse"
	RoleSupport   Role = "support"
)

func (r Role) Valid() bool { return r == RoleBuyer || r == RoleWarehouse || r == RoleSupport }

type Employee struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  Role   `json:"role"`
	// IsTeamLead lets the employee hand orders over to other employees.
	IsTeamLead bool `json:"isTeamLead"`
}

type Client struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Phone    string  `json:"phone"`
	Telegram *string `json:"telegram,omitempty"`
	City     string  `json:"city"`
	Branch   string  `json:"branch"`
	Sizes    string  `json:"sizes"`
	Stores   string  `json:"stores"`
	Dislikes *string `json:"dislikes,omitempty"`
	Note     *string `json:"note,omitempty"`
}

type Item struct {
	ID         string  `json:"id"`
	Photo      string  `json:"photo"`
	Name       string  `json:"name"`
	Store      string  `json:"store"`
	URL        string  `json:"url"`
	Size       string  `json:"size"`
	Qty        int     `json:"qty"`
	Price      *Money  `json:"price,omitempty"`
	StorePrice *Money  `json:"storePrice,omitempty"`
	Comment    *string `json:"comment,omitempty"`
	// ListingPrice is the store's price as shown in the listing, in any currency (“$49.99”),
	// kept when it can't be turned into hryvnias.
	ListingPrice  *string    `json:"listingPrice,omitempty"`
	Status        ItemStatus `json:"status"`
	ConfirmSentAt *time.Time `json:"confirmSentAt,omitempty"`
}

type Note struct {
	ID     string    `json:"id"`
	Author string    `json:"author"`
	At     time.Time `json:"at"`
	Text   string    `json:"text"`
}

type HistoryEvent struct {
	At   time.Time `json:"at"`
	Who  string    `json:"who"`
	What string    `json:"what"`
}

type Order struct {
	ID        string      `json:"id"`
	Number    string      `json:"number"`
	ClientID  string      `json:"clientId"`
	OwnerID   *string     `json:"ownerId,omitempty"`
	Status    OrderStatus `json:"status"`
	CreatedAt time.Time   `json:"createdAt"`
	DueAt     time.Time   `json:"dueAt"`
	Items     []Item      `json:"items"`
	Paid      *Money      `json:"paid,omitempty"`
	Fee       Money       `json:"fee"`
	Problem   *string     `json:"problem,omitempty"`
	Location  *string     `json:"location,omitempty"`
	// Photo is the look the client bought from (the pin image in the Fitkit app).
	Photo   *string        `json:"photo,omitempty"`
	Notes   []Note         `json:"notes"`
	History []HistoryEvent `json:"history"`
}

type Leg string

const (
	LegIn  Leg = "in"
	LegOut Leg = "out"
)

type TrackingEvent struct {
	At     time.Time `json:"at"`
	Title  string    `json:"title"`
	Detail *string   `json:"detail,omitempty"`
}

type Shipment struct {
	ID          string          `json:"id"`
	OrderID     string          `json:"orderId"`
	Leg         Leg             `json:"leg"`
	Store       *string         `json:"store,omitempty"`
	Carrier     *string         `json:"carrier,omitempty"`
	Tracking    *string         `json:"tracking,omitempty"`
	Source      *string         `json:"source,omitempty"`
	Problem     *string         `json:"problem,omitempty"`
	AtWarehouse bool            `json:"atWarehouse"`
	Delivered   bool            `json:"delivered"`
	CreatedAt   time.Time       `json:"createdAt"`
	Events      []TrackingEvent `json:"events"`
}

type Message struct {
	ID   string    `json:"id"`
	From string    `json:"from"` // client | support | system
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type Ticket struct {
	ID        string    `json:"id"`
	ClientID  string    `json:"clientId"`
	OrderID   *string   `json:"orderId,omitempty"`
	Status    string    `json:"status"` // open | resolved
	Summary   string    `json:"summary"`
	UpdatedAt time.Time `json:"updatedAt"`
	Messages  []Message `json:"messages"`
}
