package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// NovaPoshta creates internet documents (ТТН) through the Nova Poshta API 2.0.
// Docs: https://developers.novaposhta.ua — model InternetDocument, method save.
// Sender refs (counterparty, contact, address) come from the Fitkit business account.
type NovaPoshta struct {
	APIKey          string
	SenderRef       string // counterparty
	ContactRef      string // sender contact person
	SenderAddress   string // warehouse ref
	SenderCityRef   string
	SenderPhone     string
	RecipientLookup func(ctx context.Context, city, branch string) (cityRef, warehouseRef string, err error)
	HTTP            *http.Client
	URL             string
}

type npResponse struct {
	Success bool `json:"success"`
	Data    []struct {
		IntDocNumber string `json:"IntDocNumber"`
	} `json:"data"`
	Errors []string `json:"errors"`
}

func (n *NovaPoshta) CreateShipment(ctx context.Context, r ShipmentRequest) (string, error) {
	if n.RecipientLookup == nil {
		return "", fmt.Errorf("nova poshta: recipient lookup is not configured")
	}
	cityRef, whRef, err := n.RecipientLookup(ctx, r.City, r.Branch)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"apiKey":       n.APIKey,
		"modelName":    "InternetDocument",
		"calledMethod": "save",
		"methodProperties": map[string]any{
			"PayerType":             "Recipient",
			"PaymentMethod":         "Cash",
			"DateTime":              time.Now().Format("02.01.2006"),
			"CargoType":             "Parcel",
			"Weight":                "1",
			"ServiceType":           "WarehouseWarehouse",
			"SeatsAmount":           "1",
			"Description":           r.Description,
			"Cost":                  fmt.Sprint(r.Declared / 100),
			"CitySender":            n.SenderCityRef,
			"Sender":                n.SenderRef,
			"SenderAddress":         n.SenderAddress,
			"ContactSender":         n.ContactRef,
			"SendersPhone":          n.SenderPhone,
			"CityRecipient":         cityRef,
			"RecipientAddress":      whRef,
			"RecipientsPhone":       r.Phone,
			"RecipientName":         r.Recipient,
			"RecipientType":         "PrivatePerson",
			"InfoRegClientBarcodes": r.OrderNumber,
		},
	}
	buf, _ := json.Marshal(body)
	url := n.URL
	if url == "" {
		url = "https://api.novaposhta.ua/v2.0/json/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := n.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("nova poshta: %w", err)
	}
	defer resp.Body.Close()
	var out npResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("nova poshta: decode: %w", err)
	}
	if !out.Success || len(out.Data) == 0 {
		return "", fmt.Errorf("nova poshta: %v", out.Errors)
	}
	return out.Data[0].IntDocNumber, nil
}
