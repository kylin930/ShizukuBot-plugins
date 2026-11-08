package endfield

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxAPIResponseBytes = 16 << 20

var httpClient = &http.Client{Timeout: 25 * time.Second}

// apiResponse 是统一后端的响应外壳。
type apiResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	raw     json.RawMessage
}

func (r *apiResponse) decode(v any) error {
	if r == nil || len(r.Data) == 0 {
		return fmt.Errorf("empty response data")
	}
	return json.Unmarshal(r.Data, v)
}

// ok 对齐上游的 res.code === 0 判断。
func (r *apiResponse) ok() bool { return r != nil && r.Code == 0 }

type apiClient struct {
	base           string
	apiKey         string
	frameworkToken string
	roleID         string
	serverID       int
}

func newAPIClient(cfg config, binding binding) *apiClient {
	base := strings.TrimRight(cfg.BackendBaseURL, "/")
	if base == "" {
		base = "https://end-api.shallow.ink"
	}
	server := binding.serverID()
	return &apiClient{
		base:           base,
		apiKey:         cfg.APIKey,
		frameworkToken: binding.FrameworkToken,
		roleID:         binding.roleID(),
		serverID:       server,
	}
}

// hasAPIKey 对齐上游「未配置 api_key」检查。
func (c config) hasAPIKey() bool { return strings.TrimSpace(c.APIKey) != "" }

func (a *apiClient) headers() http.Header {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		header.Set("X-API-Key", a.apiKey)
	}
	if a.frameworkToken != "" {
		header.Set("X-Framework-Token", a.frameworkToken)
	}
	return header
}

func (a *apiClient) request(ctx context.Context, method, endpoint string, query url.Values, body any) (*apiResponse, error) {
	requestURL := a.base + endpoint
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return nil, err
	}
	req.Header = a.headers()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: HTTP %d %s", endpoint, resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	response := &apiResponse{raw: data}
	if err := json.Unmarshal(data, response); err != nil {
		return nil, fmt.Errorf("%s: %w", endpoint, err)
	}
	return response, nil
}

// getData 调用需要 framework_token 的玩家数据接口。
func (a *apiClient) getData(ctx context.Context, apiType string, params map[string]any) (*apiResponse, error) {
	endpoint, query, body, method, err := a.urlFor(apiType, params)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(apiType, "friend_") {
		return a.request(ctx, method, endpoint, query, body)
	}
	if a.frameworkToken == "" {
		return nil, fmt.Errorf("missing framework token")
	}
	return a.request(ctx, method, endpoint, query, body)
}

// getWikiData 与 getAnnouncementsData 只需要 API Key。
func (a *apiClient) getWikiData(ctx context.Context, apiType string, params map[string]any) (*apiResponse, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("missing api key")
	}
	endpoint, query := wikiURL(apiType, params)
	if endpoint == "" {
		return nil, fmt.Errorf("unknown wiki api: %s", apiType)
	}
	return a.request(ctx, http.MethodGet, endpoint, query, nil)
}

func (a *apiClient) getAnnouncementsData(ctx context.Context, apiType string, params map[string]any) (*apiResponse, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("missing api key")
	}
	endpoint, query := announcementURL(apiType, params)
	if endpoint == "" {
		return nil, fmt.Errorf("unknown announcement api: %s", apiType)
	}
	return a.request(ctx, http.MethodGet, endpoint, query, nil)
}

// urlFor 移植上游 EndfieldApi.getUrlMap。
func (a *apiClient) urlFor(apiType string, params map[string]any) (string, url.Values, any, string, error) {
	query := url.Values{}
	roleID := stringParam(params, "roleId", a.roleID)
	serverID := intParam(params, "serverId", a.serverID)
	setRoleServer := func() {
		if roleID != "" {
			query.Set("roleId", roleID)
			query.Set("serverId", strconv.Itoa(serverID))
		}
	}
	method := http.MethodGet
	var body any
	switch apiType {
	case "user_info":
		return "/api/endfield/user", query, nil, method, nil
	case "binding":
		return "/api/endfield/binding", query, nil, method, nil
	case "friend_health":
		return "/api/friend/health", query, nil, method, nil
	case "friend_detail":
		if id := stringParam(params, "role_id", roleID); id != "" {
			query.Set("role_id", id)
		}
		return "/api/friend/detail", query, nil, method, nil
	case "friend_char":
		if id := stringParam(params, "role_id", roleID); id != "" {
			query.Set("role_id", id)
		}
		if template := stringParam(params, "template_id", ""); template != "" {
			query.Set("template_id", template)
		}
		return "/api/friend/char", query, nil, method, nil
	case "endfield_attendance":
		method = http.MethodPost
		body = params
		return "/api/endfield/attendance", query, body, method, nil
	case "endfield_card_detail":
		query.Set("roleId", stringParam(params, "roleId", a.roleID))
		query.Set("serverId", strconv.Itoa(intParam(params, "serverId", a.serverID)))
		query.Set("userId", stringParam(params, "userId", a.roleID))
		return "/api/endfield/card/detail", query, nil, method, nil
	case "endfield_card_char":
		if instID := stringParam(params, "instId", ""); instID != "" {
			query.Set("instId", instID)
		} else {
			if operatorID := stringParam(params, "operatorId", ""); operatorID != "" {
				query.Set("operatorId", operatorID)
			}
			if charID := stringParam(params, "charId", ""); charID != "" {
				query.Set("charId", charID)
			}
		}
		if id := stringParam(params, "roleId", ""); id != "" {
			query.Set("roleId", id)
		}
		if id := intParam(params, "serverId", 0); id > 0 {
			query.Set("serverId", strconv.Itoa(id))
		}
		return "/api/endfield/card/char", query, nil, method, nil
	case "endfield_search_chars":
		return "/api/endfield/search/chars", query, nil, method, nil
	case "endfield_search_weapons":
		return "/api/endfield/search/weapons", query, nil, method, nil
	case "endfield_search_equipments":
		return "/api/endfield/search/equipments", query, nil, method, nil
	case "endfield_search_tactical_items":
		return "/api/endfield/search/tactical-items", query, nil, method, nil
	case "stamina":
		setRoleServer()
		return "/api/endfield/stamina", query, nil, method, nil
	case "spaceship":
		setRoleServer()
		return "/api/endfield/spaceship", query, nil, method, nil
	case "note":
		setRoleServer()
		return "/api/endfield/note", query, nil, method, nil
	case "cultivate_zone":
		setRoleServer()
		return "/api/endfield/domain", query, nil, method, nil
	case "panel_sync":
		method = http.MethodPost
		body = params
		return "/api/panel/sync", query, body, method, nil
	case "panel_sync_status":
		return "/api/panel/sync/status", query, nil, method, nil
	case "panel_chars":
		setPage(query, params)
		return "/api/panel/chars", query, nil, method, nil
	case "panel_char_detail":
		template := stringParam(params, "template_id", "")
		if template == "" {
			template = stringParam(params, "templateId", "")
		}
		return "/api/panel/char/" + url.PathEscape(template), query, nil, method, nil
	case "panel_chars_all":
		setPage(query, params)
		return "/api/panel/chars/all", query, nil, method, nil
	case "achieve":
		setRoleServer()
		return "/api/endfield/achieve", query, nil, method, nil
	case "indie_hard":
		setRoleServer()
		return "/api/endfield/card/indie-hard", query, nil, method, nil
	case "crisis_contract":
		if contract := stringParam(params, "contractId", stringParam(params, "contract_id", "")); contract != "" {
			query.Set("contractId", contract)
		}
		id := stringParam(params, "roleId", stringParam(params, "role_id", ""))
		if id != "" {
			query.Set("roleId", id)
		}
		if id != "" || stringParam(params, "serverId", stringParam(params, "server_id", "")) != "" {
			server := stringParam(params, "serverId", stringParam(params, "server_id", ""))
			if server == "" {
				server = "1"
			}
			query.Set("serverId", server)
		}
		return "/api/endfield/card/crisis-contract", query, nil, method, nil
	default:
		return "", nil, nil, "", fmt.Errorf("unknown api type: %s", apiType)
	}
}

func wikiURL(apiType string, params map[string]any) (string, url.Values) {
	query := url.Values{}
	keyword := stringParam(params, "q", stringParam(params, "keyword", ""))
	if keyword != "" {
		query.Set("q", keyword)
	}
	if mainType := stringParam(params, "main_type_id", ""); mainType != "" {
		query.Set("main_type_id", mainType)
	}
	if subType := stringParam(params, "sub_type_id", ""); subType != "" {
		query.Set("sub_type_id", subType)
	}
	query.Set("page", strconv.Itoa(intParam(params, "page", 1)))
	query.Set("page_size", strconv.Itoa(intParam(params, "page_size", 20)))
	switch apiType {
	case "wiki_search":
		return "/api/wiki/search", query
	case "wiki_items":
		return "/api/wiki/items", query
	case "wiki_item_detail":
		return "/api/wiki/items/" + url.PathEscape(stringParam(params, "id", "")), url.Values{}
	case "wiki_activities":
		return "/api/wiki/activities", url.Values{}
	case "bili_wiki_activities":
		return "/api/bili-wiki/activities", url.Values{}
	default:
		return "", nil
	}
}

func announcementURL(apiType string, params map[string]any) (string, url.Values) {
	query := url.Values{}
	switch apiType {
	case "announcements_list":
		page := intParam(params, "page", 1)
		if page < 1 {
			page = 1
		}
		size := intParam(params, "page_size", 20)
		if size < 1 {
			size = 1
		}
		if size > 100 {
			size = 100
		}
		query.Set("page", strconv.Itoa(page))
		query.Set("page_size", strconv.Itoa(size))
		return "/api/announcements", query
	case "announcements_latest":
		return "/api/announcements/latest", query
	case "announcement_detail":
		id := stringParam(params, "id", stringParam(params, "item_id", ""))
		return "/api/announcements/" + url.PathEscape(id), url.Values{}
	default:
		return "", nil
	}
}

func setPage(query url.Values, params map[string]any) {
	page := intParam(params, "page", 1)
	if page < 1 {
		page = 1
	}
	size := intParam(params, "page_size", 20)
	if size < 1 {
		size = 1
	}
	if size > 50 {
		size = 50
	}
	query.Set("page", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(size))
}

func stringParam(params map[string]any, key, fallback string) string {
	value, ok := params[key]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return fallback
		}
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	default:
		return fmt.Sprint(v)
	}
}

func intParam(params map[string]any, key string, fallback int) int {
	value, ok := params[key]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return fallback
}
