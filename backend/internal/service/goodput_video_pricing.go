package service

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"time"
)

const goodPutVideoCatalogURL = "https://goodputai.cn/api/v1/public/models"

var goodPutDecimalPrice = regexp.MustCompile(`^[0-9]{1,6}(\.[0-9]{1,18})?$`)

func (s *OpenAIGatewayService) checkGoodPutVideoCatalogPrice(ctx context.Context, account *Account, model string) error {
	paused := &GoodPutVideoError{Status: 503, Code: "goodput_video_price_unavailable", Message: "GoodPut H3 is paused: its CNY promotion price could not be verified at or below 0.09 per second"}
	if _, err := s.validateUpstreamBaseURL(goodPutVideoCatalogURL); err != nil {
		return paused
	}
	proxy, err := ResolveAccountProxyURL(account)
	if err != nil {
		return paused
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	ctx = WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, goodPutVideoCatalogURL, nil)
	if err != nil {
		return paused
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return paused
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || !goodPutVideoCatalogAllows(resp.Body, model) {
		return paused
	}
	return nil
}

func goodPutVideoCatalogAllows(reader io.Reader, model string) bool {
	const maxCatalogBytes = 256 << 10
	body, err := io.ReadAll(io.LimitReader(reader, maxCatalogBytes+1))
	if err != nil || len(body) > maxCatalogBytes {
		return false
	}
	var catalog struct {
		Models []struct {
			Model string          `json:"public_model"`
			Price json.RawMessage `json:"price"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		return false
	}
	ceiling := big.NewRat(9, 100)
	found := false
	for _, item := range catalog.Models {
		if item.Model != model {
			continue
		}
		if found || !goodPutVideoPriceAllows(item.Price, ceiling) {
			return false
		}
		found = true
	}
	return found
}

func goodPutVideoPriceAllows(data json.RawMessage, ceiling *big.Rat) bool {
	var price struct {
		Mode     string                     `json:"mode"`
		Currency string                     `json:"currency"`
		Seconds  string                     `json:"per_video_second"`
		ResTiers map[string]json.RawMessage `json:"per_video_second_tiers"`
		Tiers    []json.RawMessage          `json:"tiers"`
		Windows  []json.RawMessage          `json:"windows"`
		Image    *json.RawMessage           `json:"per_image"`
		Chars    *json.RawMessage           `json:"per_1k_characters"`
		Input    *json.RawMessage           `json:"input_per_1m"`
		Output   *json.RawMessage           `json:"output_per_1m"`
		Read     *json.RawMessage           `json:"cache_read_per_1m"`
		Cache    *json.RawMessage           `json:"cache_creation_per_1m"`
		Meters   []struct {
			Meter string `json:"meter"`
			Unit  string `json:"unit"`
			Price string `json:"price_per_unit"`
		} `json:"meter_prices"`
	}
	if err := json.Unmarshal(data, &price); err != nil || price.Mode != "meter" || price.Currency != "CNY" ||
		len(price.ResTiers)+len(price.Tiers)+len(price.Windows) != 0 || len(price.Meters) != 1 ||
		price.Image != nil || price.Chars != nil || price.Input != nil || price.Output != nil || price.Read != nil || price.Cache != nil {
		return false
	}
	m := price.Meters[0]
	if m.Meter != "video_seconds" || m.Unit != "second" || !goodPutDecimalPrice.MatchString(price.Seconds) || !goodPutDecimalPrice.MatchString(m.Price) {
		return false
	}
	rate, ok := new(big.Rat).SetString(price.Seconds)
	meter, parsed := new(big.Rat).SetString(m.Price)
	return ok && parsed && rate.Sign() > 0 && rate.Cmp(ceiling) <= 0 && rate.Cmp(meter) == 0
}
