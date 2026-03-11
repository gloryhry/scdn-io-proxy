package scdn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	apiKey string
	hc     *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		hc: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

type GetProxyRequest struct {
	Protocol    string
	CountryCode string
	Count       int
}

type GetProxyResponse struct {
	Proxies []string
}

type apiResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Proxies []string `json:"proxies"`
	} `json:"data"`
}

func (c *Client) GetProxy(ctx context.Context, req GetProxyRequest) (GetProxyResponse, error) {
	if c == nil {
		return GetProxyResponse{}, fmt.Errorf("client 为空")
	}
	u, err := url.Parse("https://proxy.scdn.io/api/get_proxy.php")
	if err != nil {
		return GetProxyResponse{}, err
	}
	q := u.Query()
	if c.apiKey != "" {
		q.Set("api_key", c.apiKey)
	}
	q.Set("protocol", req.Protocol)
	q.Set("count", fmt.Sprintf("%d", req.Count))
	q.Set("country_code", req.CountryCode)
	u.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return GetProxyResponse{}, err
	}
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return GetProxyResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return GetProxyResponse{}, fmt.Errorf("SCDN HTTP状态码异常: %s", resp.Status)
	}

	var ar apiResp
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return GetProxyResponse{}, err
	}
	if ar.Code != 200 {
		if ar.Message == "" {
			ar.Message = "unknown error"
		}
		return GetProxyResponse{}, fmt.Errorf("SCDN API错误: code=%d message=%s", ar.Code, ar.Message)
	}
	return GetProxyResponse{Proxies: ar.Data.Proxies}, nil
}
