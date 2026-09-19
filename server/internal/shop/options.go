package shop

import (
	"context"
	"errors"
	"sync"
)

// ListingOptions is what the app shows before checkout for one listing: how
// it checks out and, for Shopify, the sizes and colors to pick from.
type ListingOptions struct {
	URL      string   `json:"url"`
	Merchant string   `json:"merchant"`
	Method   Method   `json:"method"`
	Product  *Product `json:"product,omitempty"`
}

// Options classifies listings and loads Shopify variants, a few at a time.
// A store that can't be reached is reported as a plain product page.
func (s *Service) Options(ctx context.Context, urls []string) ([]ListingOptions, error) {
	out := make([]ListingOptions, len(urls))
	errs := make([]error, len(urls))
	sem := make(chan struct{}, fetchWorkers)
	var wg sync.WaitGroup
	for i, raw := range urls {
		l, err := Classify(raw)
		if err != nil {
			out[i] = ListingOptions{URL: raw, Method: MethodProductPage}
			continue
		}
		out[i] = ListingOptions{URL: raw, Merchant: l.Merchant, Method: l.Method}
		if l.ShopifyHandle == "" {
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			product, err := s.ShopifyProduct(ctx, l)
			if err != nil && !errors.Is(err, ErrUnavailable) {
				errs[i] = err
				return
			}
			if product != nil {
				out[i].Method = MethodShopify
				out[i].Product = product
			}
		})
	}
	wg.Wait()
	return out, errors.Join(errs...)
}
