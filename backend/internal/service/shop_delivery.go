package service

import (
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const shopDeliveryTimeLayout = "2006-01-02 15:04:05"

var shopDeliveryBeijing = time.FixedZone("Asia/Shanghai", 8*60*60)

func shopDeliveryExpiry(expires time.Time) string {
	return "到期时间：" + expires.In(shopDeliveryBeijing).Format(shopDeliveryTimeLayout) + "（北京时间 UTC+8）"
}

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
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "到期时间："); ok {
				if utc, ok := strings.CutSuffix(value, "（UTC）"); ok {
					if expires, err := time.Parse(shopDeliveryTimeLayout, utc); err == nil {
						line = shopDeliveryExpiry(expires)
					}
				}
			}
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
