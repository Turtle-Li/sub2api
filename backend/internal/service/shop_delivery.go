package service

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func normalizeShopDeliveryConfig(p *ShopProduct) error {
	if p.DeliveryConfig == nil || !p.DeliveryConfig.IndividualCopy {
		p.DeliveryConfig = nil
		return nil
	}
	if p.FulfillmentMode != ShopAccountDelivery || shopAutomaticFulfillment(*p) {
		return infraerrors.BadRequest("SHOP_DELIVERY_CONFIG_INVALID", "逐项复制仅适用于人工交付的成品号商品")
	}
	return nil
}

func shopDeliveryContent(p ShopProduct, content string) string {
	if !nativeShopProduct(p) {
		return content
	}
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case "无需注册或登录New API。", "无需注册或登录 New API。":
			continue
		default:
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
