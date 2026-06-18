package cms

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ParsedPage はHTMLファイルから抽出したすべてのインデックス用データを表します。
type ParsedPage struct {
	ID                string
	Title             string
	Tags              []PageTag
	ClientOrders      []ClientOrder
	OurOrders         []OurOrder
	OurEstimates      []OurEstimate
	SupplierEstimates []SupplierEstimate
}

type PageTag struct {
	Name  string
	Value string
}

type ClientOrder struct {
	OrderNo    string
	ClientName string
	PDFPath    string
	OrderedAt  string
	Items      []ClientOrderItem
}

type ClientOrderItem struct {
	ItemID   string
	ItemName string
	Price    int
	Quantity int
	Status   string
}

type OurOrder struct {
	OrderNo      string
	SupplierName string
	PDFPath      string
	OrderedAt    string
	Items        []OurOrderItem
}

type OurOrderItem struct {
	ItemName string
	Cost     int
	Quantity int
	Status   string
}

type OurEstimate struct {
	ItemID      string
	ClientName  string
	Price       int
	PDFPath     string
	EstimatedAt string
}

type SupplierEstimate struct {
	ItemName     string
	SupplierName string
	Cost         int
	PDFPath      string
	EstimatedAt  string
}

// ParseHTMLMaster はHTML5の仕様に従って文字列を解析し、検索に必要な情報を抽出します。
func ParseHTMLMaster(id string, htmlContent string) ParsedPage {
	parsed := ParsedPage{
		ID:    id,
		Title: "No Title",
	}

	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return parsed
	}

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// タイトル(h1)の抽出
			if n.Data == "h1" && parsed.Title == "No Title" {
				parsed.Title = extractText(n)
			}

			// <m-tag> の解析
			if n.Data == "m-tag" {
				var tag PageTag
				for _, attr := range n.Attr {
					if attr.Key == "name" {
						tag.Name = attr.Val
					}
					if attr.Key == "value" {
						tag.Value = attr.Val
					}
				}
				if tag.Name != "" {
					parsed.Tags = append(parsed.Tags, tag)
				}
			}

			// <m-file> の解析
			if n.Data == "m-file" {
				var fileTag string
				var src, orderNo, clientName, supplierName, itemID, itemName, dateStr string
				var price, cost int

				for _, attr := range n.Attr {
					switch attr.Key {
					case "tag":
						fileTag = attr.Val
					case "src":
						src = attr.Val
					case "order-no":
						orderNo = attr.Val
					case "client-name":
						clientName = attr.Val
					case "supplier-name":
						supplierName = attr.Val
					case "item-id":
						itemID = attr.Val
					case "item-name":
						itemName = attr.Val
					case "price":
						price, _ = strconv.Atoi(attr.Val)
					case "cost":
						cost, _ = strconv.Atoi(attr.Val)
					case "ordered-at":
						dateStr = attr.Val
					case "estimated-at":
						dateStr = attr.Val
					}
				}

				switch fileTag {
				case "弊社の見積もり":
					parsed.OurEstimates = append(parsed.OurEstimates, OurEstimate{
						ItemID:      itemID,
						ClientName:  clientName,
						Price:       price,
						PDFPath:     src,
						EstimatedAt: dateStr,
					})
				case "材料屋・加工業者の見積もり":
					parsed.SupplierEstimates = append(parsed.SupplierEstimates, SupplierEstimate{
						ItemName:     itemName,
						SupplierName: supplierName,
						Cost:         cost,
						PDFPath:      src,
						EstimatedAt:  dateStr,
					})
				case "顧客の発注書":
					order := ClientOrder{
						OrderNo:    orderNo,
						ClientName: clientName,
						PDFPath:    src,
						OrderedAt:  dateStr,
					}
					order.Items = parseClientOrderItems(n)
					parsed.ClientOrders = append(parsed.ClientOrders, order)
				case "弊社の発注書":
					order := OurOrder{
						OrderNo:      orderNo,
						SupplierName: supplierName,
						PDFPath:      src,
						OrderedAt:    dateStr,
					}
					order.Items = parseOurOrderItems(n)
					parsed.OurOrders = append(parsed.OurOrders, order)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)
	return parsed
}

// parseClientOrderItems は <m-file tag="顧客の発注書"> の子要素である <m-item> をパースします。
func parseClientOrderItems(parent *html.Node) []ClientOrderItem {
	var items []ClientOrderItem
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "m-item" {
			var item ClientOrderItem
			item.Quantity = 1 // デフォルト値
			for _, attr := range c.Attr {
				switch attr.Key {
				case "item-id":
					item.ItemID = attr.Val
				case "item-name":
					item.ItemName = attr.Val
				case "price":
					item.Price, _ = strconv.Atoi(attr.Val)
				case "quantity":
					item.Quantity, _ = strconv.Atoi(attr.Val)
				case "status":
					item.Status = attr.Val
				}
			}
			items = append(items, item)
		}
	}
	return items
}

// parseOurOrderItems は <m-file tag="弊社の発注書"> の子要素である <m-item> をパースします。
func parseOurOrderItems(parent *html.Node) []OurOrderItem {
	var items []OurOrderItem
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "m-item" {
			var item OurOrderItem
			item.Quantity = 1 // デフォルト値
			for _, attr := range c.Attr {
				switch attr.Key {
				case "item-name":
					item.ItemName = attr.Val
				case "cost":
					item.Cost, _ = strconv.Atoi(attr.Val)
				case "quantity":
					item.Quantity, _ = strconv.Atoi(attr.Val)
				case "status":
					item.Status = attr.Val
				}
			}
			items = append(items, item)
		}
	}
	return items
}

// extractText は指定されたHTMLノード配下にあるテキストをすべて抽出し、結合して返す関数です。
func extractText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var result string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		result += extractText(c)
	}
	return strings.TrimSpace(result)
}
