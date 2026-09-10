package payment

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	ServerKey  string
	IsProd     bool
	HTTPClient *http.Client
}

func NewClient(serverKey string, isProd bool) *Client {
	return &Client{
		ServerKey:  serverKey,
		IsProd:     isProd,
		HTTPClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) baseURL() string {
	if c.IsProd {
		return "https://api.midtrans.com"
	}
	return "https://api.sandbox.midtrans.com"
}

type QRISChargeRequest struct {
	OrderID     string
	AmountIDR   int64
	CustomerName string
	Email       string
	ItemName    string
}

type QRISChargeResult struct {
	OrderID         string
	TransactionID   string
	QRString        string
	TransactionStatus string
	Raw             string
	Mock            bool
}

type midtransChargePayload struct {
	PaymentType        string `json:"payment_type"`
	TransactionDetails struct {
		OrderID     string `json:"order_id"`
		GrossAmount int64  `json:"gross_amount"`
	} `json:"transaction_details"`
	ItemDetails []struct {
		ID       string `json:"id"`
		Price    int64  `json:"price"`
		Quantity int    `json:"quantity"`
		Name     string `json:"name"`
	} `json:"item_details"`
	CustomerDetails struct {
		FirstName string `json:"first_name"`
		Email     string `json:"email"`
	} `json:"customer_details"`
	Qris struct {
		Acquirer string `json:"acquirer"`
	} `json:"qris"`
}

type midtransChargeResponse struct {
	TransactionID     string `json:"transaction_id"`
	OrderID           string `json:"order_id"`
	TransactionStatus string `json:"transaction_status"`
	Actions           []struct {
		Name   string `json:"name"`
		Method string `json:"method"`
		URL    string `json:"url"`
	} `json:"actions"`
	QRString string `json:"qr_string"`
	StatusMessage string `json:"status_message"`
}

// ChargeQRIS creates a Midtrans QRIS charge. If ServerKey is empty, returns a local mock QR for development.
func (c *Client) ChargeQRIS(ctx context.Context, req QRISChargeRequest) (*QRISChargeResult, error) {
	if strings.TrimSpace(c.ServerKey) == "" {
		qr := fmt.Sprintf("MOCK-QRIS|order=%s|amount=%d", req.OrderID, req.AmountIDR)
		raw, _ := json.Marshal(map[string]any{
			"mock": true, "order_id": req.OrderID, "qr_string": qr, "gross_amount": req.AmountIDR,
		})
		return &QRISChargeResult{
			OrderID:           req.OrderID,
			TransactionID:     "mock-" + req.OrderID,
			QRString:          qr,
			TransactionStatus: "pending",
			Raw:               string(raw),
			Mock:              true,
		}, nil
	}

	payload := midtransChargePayload{PaymentType: "qris"}
	payload.TransactionDetails.OrderID = req.OrderID
	payload.TransactionDetails.GrossAmount = req.AmountIDR
	payload.ItemDetails = []struct {
		ID       string `json:"id"`
		Price    int64  `json:"price"`
		Quantity int    `json:"quantity"`
		Name     string `json:"name"`
	}{{ID: "service", Price: req.AmountIDR, Quantity: 1, Name: req.ItemName}}
	payload.CustomerDetails.FirstName = req.CustomerName
	payload.CustomerDetails.Email = req.Email
	payload.Qris.Acquirer = "gopay"

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/v2/charge", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.SetBasicAuth(c.ServerKey, "")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("midtrans charge failed (%d): %s", resp.StatusCode, string(raw))
	}
	var parsed midtransChargeResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	qr := parsed.QRString
	if qr == "" {
		for _, a := range parsed.Actions {
			if strings.EqualFold(a.Name, "generate-qr-code") || strings.Contains(strings.ToLower(a.Name), "qr") {
				qr = a.URL
				break
			}
		}
	}
	return &QRISChargeResult{
		OrderID:           parsed.OrderID,
		TransactionID:     parsed.TransactionID,
		QRString:          qr,
		TransactionStatus: parsed.TransactionStatus,
		Raw:               string(raw),
	}, nil
}

type Notification struct {
	OrderID           string `json:"order_id"`
	TransactionID     string `json:"transaction_id"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	StatusCode        string `json:"status_code"`
	GrossAmount       string `json:"gross_amount"`
	SignatureKey      string `json:"signature_key"`
	PaymentType       string `json:"payment_type"`
}

func (c *Client) VerifySignature(n Notification) bool {
	if strings.TrimSpace(c.ServerKey) == "" {
		// Dev mock: accept any notification when keys are unset.
		return true
	}
	payload := n.OrderID + n.StatusCode + n.GrossAmount + c.ServerKey
	sum := sha512.Sum512([]byte(payload))
	return hex.EncodeToString(sum[:]) == n.SignatureKey
}

func IsPaidStatus(transactionStatus, fraudStatus string) bool {
	switch transactionStatus {
	case "capture":
		return fraudStatus == "" || fraudStatus == "accept"
	case "settlement":
		return true
	default:
		return false
	}
}

func IsFailureStatus(transactionStatus string) bool {
	switch transactionStatus {
	case "deny", "cancel", "expire", "failure":
		return true
	default:
		return false
	}
}
