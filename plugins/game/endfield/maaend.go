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

	"shizukubot/sdk"
)

const (
	maaKeyPrefix = "endfield:maa:"
)

type maaStore struct {
	DeviceIDs       []string `json:"device_ids,omitempty"`
	DefaultDeviceID string   `json:"default_device_id,omitempty"`
	JobIDs          []string `json:"job_ids,omitempty"`
}

type maaDeviceCapabilities struct {
	Tasks []string `json:"tasks"`
}

type maaDevice struct {
	DeviceID      string                `json:"device_id"`
	DeviceName    string                `json:"device_name"`
	Status        string                `json:"status"`
	MaaendVersion string                `json:"maaend_version"`
	ClientVersion string                `json:"client_version"`
	CurrentJobID  string                `json:"current_job_id"`
	Capabilities  maaDeviceCapabilities `json:"capabilities"`
}

type maaTaskOptionCase struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

type maaTaskOption struct {
	Name  string              `json:"name"`
	Label string              `json:"label"`
	Cases []maaTaskOptionCase `json:"cases"`
}

type maaTask struct {
	Name        string          `json:"name"`
	Label       string          `json:"label"`
	Description string          `json:"description"`
	Options     []maaTaskOption `json:"options"`
}

type maaTaskList struct {
	Tasks       []maaTask `json:"tasks"`
	Controllers []string  `json:"controllers"`
	Resources   []string  `json:"resources"`
}

type maaJobProgress struct {
	Completed int `json:"completed"`
	Total     int `json:"total"`
}

type maaJobLog struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

type maaJob struct {
	JobID       string         `json:"job_id"`
	DeviceID    string         `json:"device_id"`
	DeviceName  string         `json:"device_name"`
	Status      string         `json:"status"`
	CurrentTask string         `json:"current_task"`
	Progress    maaJobProgress `json:"progress"`
	DurationMS  int64          `json:"duration_ms"`
	Error       string         `json:"error"`
	Logs        []maaJobLog    `json:"logs"`
}

type maaResponse struct {
	OK      bool
	Status  int
	Code    int
	Message string
	Data    json.RawMessage
}

func (r *maaResponse) decode(v any) error {
	if r == nil || len(r.Data) == 0 {
		return fmt.Errorf("empty response data")
	}
	return json.Unmarshal(r.Data, v)
}

func init() {
	registerRoute(108, prefixMatch("maa 绑定", "maa绑定"), handleMaaBind)
	registerRoute(108, prefixMatch("maa 设置设备", "maa设置设备"), handleMaaSetDevice)
	registerRoute(108, prefixMatch("maa 任务列表", "maa任务列表", "maa 设备任务", "maa设备任务"), handleMaaTaskList)
	registerRoute(108, prefixMatch("maa 删除设备", "maa删除设备"), handleMaaDelete)
	registerRoute(108, prefixMatch("maa 设备", "maa设备"), handleMaaDevices)
	registerRoute(108, prefixMatch("maa 执行", "maa执行", "maa 运行", "maa运行"), handleMaaExec)
	registerRoute(108, prefixMatch("maa 状态", "maa状态"), handleMaaStatus)
	registerRoute(108, prefixMatch("maa 停止", "maa停止"), handleMaaStop)
	registerRoute(108, prefixMatch("maa 截图", "maa截图"), handleMaaScreenshot)
	registerRoute(108, prefixMatch("maa 重置", "maa重置"), handleMaaReset)
	registerRoute(108, prefixMatch("maa 历史", "maa历史"), handleMaaHistory)
}

func maaKey(userID string) string { return maaKeyPrefix + userID }

func maaLoadStore(ctx context.Context, st sdk.State, userID string) maaStore {
	var store maaStore
	_ = sdk.LoadJSON(ctx, st, maaKey(userID), &store)
	if store.DeviceIDs == nil {
		store.DeviceIDs = []string{}
	}
	if store.JobIDs == nil {
		store.JobIDs = []string{}
	}
	return store
}

func maaSaveStore(ctx context.Context, st sdk.State, userID string, store maaStore) {
	_ = sdk.SaveJSON(ctx, st, maaKey(userID), store)
}

func maaRequest(ctx context.Context, cfg config, method, path string, body any, acceptImage bool) (*maaResponse, []byte, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, nil, fmt.Errorf("missing api key")
	}
	base := strings.TrimRight(cfg.BackendBaseURL, "/")
	if base == "" {
		base = "https://end-api.shallow.ink"
	}
	requestURL := base + path
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("X-API-Key", strings.TrimSpace(cfg.APIKey))
	req.Header.Set("Content-Type", "application/json")
	if acceptImage {
		req.Header.Set("Accept", "image/png")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	contentType := resp.Header.Get("Content-Type")
	if acceptImage && strings.HasPrefix(strings.ToLower(contentType), "image/") {
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes))
		if readErr != nil {
			return nil, nil, readErr
		}
		return &maaResponse{OK: resp.StatusCode >= 200 && resp.StatusCode < 300, Status: resp.StatusCode}, data, nil
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes))
	if readErr != nil {
		return nil, nil, readErr
	}
	result := &maaResponse{OK: resp.StatusCode >= 200 && resp.StatusCode < 300, Status: resp.StatusCode, Code: resp.StatusCode}
	if len(data) > 0 {
		var payload struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(data, &payload); err == nil {
			result.Code = payload.Code
			result.Message = payload.Message
			result.Data = payload.Data
		}
	}
	return result, nil, nil
}

func maaIsPrivate(c sdk.Call) bool {
	return c.Event.GroupID == "" && c.Event.GuildID == ""
}

func maaFormatDuration(seconds int) string {
	if seconds <= 0 {
		return getMessage("common.placeholder")
	}
	if seconds < 60 {
		return getMessage("maaend.duration_seconds", map[string]any{"seconds": seconds})
	}
	minutes := seconds / 60
	rest := seconds % 60
	if rest > 0 {
		return getMessage("maaend.duration_minutes_seconds", map[string]any{"minutes": minutes, "seconds": rest})
	}
	return getMessage("maaend.duration_minutes", map[string]any{"minutes": minutes})
}

func maaStatusText(status string) string {
	switch status {
	case "online":
		return getMessage("maaend.status_online")
	case "offline":
		return getMessage("maaend.status_offline")
	case "busy":
		return getMessage("maaend.status_busy")
	}
	if status == "" {
		return getMessage("common.placeholder")
	}
	return status
}

func maaJobStatusText(status string) string {
	switch status {
	case "pending":
		return getMessage("maaend.job_status_pending")
	case "running":
		return getMessage("maaend.job_status_running")
	case "completed":
		return getMessage("maaend.job_status_completed")
	case "failed":
		return getMessage("maaend.job_status_failed")
	case "cancelled":
		return getMessage("maaend.job_status_cancelled")
	}
	if status == "" {
		return getMessage("common.placeholder")
	}
	return status
}

func maaFetchDevices(ctx context.Context, cfg config) ([]maaDevice, error) {
	res, _, err := maaRequest(ctx, cfg, http.MethodGet, "/api/maaend/devices", nil, false)
	if err != nil {
		return nil, err
	}
	if res == nil || !res.OK || res.Code != 0 {
		if res != nil && res.Message != "" {
			return nil, fmt.Errorf("%s", res.Message)
		}
		return nil, fmt.Errorf("device list failed")
	}
	var data struct {
		Devices []maaDevice `json:"devices"`
	}
	if err := res.decode(&data); err != nil {
		return nil, err
	}
	return data.Devices, nil
}

func maaUserDevices(ctx context.Context, cfg config, st sdk.State, userID string) ([]maaDevice, maaStore, string) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, maaStore{}, getMessage("maaend.no_api_key")
	}
	allDevices, err := maaFetchDevices(ctx, cfg)
	if err != nil {
		message := strings.TrimSpace(err.Error())
		if message == "" || message == "device list failed" {
			message = getMessage("maaend.device_list_failed")
		}
		return nil, maaStore{}, message
	}
	store := maaLoadStore(ctx, st, userID)
	existing := map[string]bool{}
	for _, device := range allDevices {
		existing[device.DeviceID] = true
	}
	validIDs := make([]string, 0, len(store.DeviceIDs))
	for _, id := range store.DeviceIDs {
		if existing[id] {
			validIDs = append(validIDs, id)
		}
	}
	if len(validIDs) != len(store.DeviceIDs) {
		store.DeviceIDs = validIDs
		maaSaveStore(ctx, st, userID, store)
	}
	validSet := map[string]bool{}
	for _, id := range validIDs {
		validSet[id] = true
	}
	devices := make([]maaDevice, 0, len(validIDs))
	for _, device := range allDevices {
		if validSet[device.DeviceID] {
			devices = append(devices, device)
		}
	}
	return devices, store, ""
}

func maaResolveDevice(ctx context.Context, cfg config, st sdk.State, userID, index string) (maaDevice, maaStore, int, string) {
	devices, store, errText := maaUserDevices(ctx, cfg, st, userID)
	if errText != "" {
		return maaDevice{}, store, 0, errText
	}
	if len(devices) == 0 {
		return maaDevice{}, store, 0, getMessage("maaend.no_devices")
	}
	index = strings.TrimSpace(index)
	if index == "" {
		if store.DefaultDeviceID == "" {
			if len(devices) == 1 {
				return devices[0], store, 1, ""
			}
			return maaDevice{}, store, 0, getMessage("maaend.no_device_index")
		}
		for position, device := range devices {
			if device.DeviceID == store.DefaultDeviceID {
				return device, store, position + 1, ""
			}
		}
		store.DefaultDeviceID = ""
		maaSaveStore(ctx, st, userID, store)
		if len(devices) == 1 {
			return devices[0], store, 1, ""
		}
		return maaDevice{}, store, 0, getMessage("maaend.default_device_invalid")
	}
	position, err := strconv.Atoi(index)
	if err != nil || position < 1 || position > len(devices) {
		return maaDevice{}, store, 0, getMessage("maaend.device_index_out", map[string]any{"max": len(devices)})
	}
	return devices[position-1], store, position, ""
}

func maaAddDevice(store *maaStore, deviceID string) {
	for _, id := range store.DeviceIDs {
		if id == deviceID {
			return
		}
	}
	store.DeviceIDs = append(store.DeviceIDs, deviceID)
}

func maaRemoveDevice(store *maaStore, deviceID string) {
	kept := make([]string, 0, len(store.DeviceIDs))
	for _, id := range store.DeviceIDs {
		if id != deviceID {
			kept = append(kept, id)
		}
	}
	store.DeviceIDs = kept
	if store.DefaultDeviceID == deviceID {
		store.DefaultDeviceID = ""
	}
}

func maaSaveJobID(store *maaStore, jobID string) {
	if strings.TrimSpace(jobID) == "" {
		return
	}
	ids := []string{jobID}
	for _, id := range store.JobIDs {
		if id != jobID {
			ids = append(ids, id)
		}
	}
	if len(ids) > 20 {
		ids = ids[:20]
	}
	store.JobIDs = ids
}

func maaResolveJobID(store maaStore, input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		input = "1"
	}
	if number, err := strconv.Atoi(input); err == nil {
		if strconv.Itoa(number) == input && number >= 1 && number <= 20 {
			if number <= len(store.JobIDs) {
				return store.JobIDs[number-1]
			}
			return ""
		}
	}
	return input
}

// ---------- 子指令 ----------

func handleMaaBind(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	if !maaIsPrivate(c) {
		return replyText(ctx, c, getMessage("maaend.bind_private_only"))
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return replyText(ctx, c, getMessage("maaend.no_api_key"))
	}
	before, _ := maaFetchDevices(ctx, cfg)
	beforeSet := map[string]bool{}
	for _, device := range before {
		beforeSet[device.DeviceID] = true
	}
	res, _, err := maaRequest(ctx, cfg, http.MethodPost, "/api/maaend/devices/bind-code", map[string]any{}, false)
	if err != nil || res == nil || !res.OK || res.Code != 0 {
		if res != nil && res.Message != "" {
			return replyText(ctx, c, res.Message)
		}
		return replyText(ctx, c, getMessage("maaend.bind_failed"))
	}
	var data struct {
		BindCode  string `json:"bind_code"`
		ExpiresIn int    `json:"expires_in"`
	}
	_ = res.decode(&data)
	code := data.BindCode
	if code == "" {
		code = getMessage("common.placeholder")
	}
	expires := data.ExpiresIn
	if expires <= 0 {
		expires = 300
	}
	text := strings.Join([]string{
		getMessage("maaend.bind_code_title"),
		getMessage("maaend.bind_code_value", map[string]any{"code": code}),
		getMessage("maaend.bind_code_expire", map[string]any{"duration": maaFormatDuration(expires)}),
		"",
		getMessage("maaend.bind_code_help"),
		getMessage("maaend.bind_code_help2"),
	}, "\n")
	if err := replyText(ctx, c, text); err != nil {
		return err
	}
	go maaPollBindDevice(cfg, c, beforeSet, expires)
	return nil
}

func maaPollBindDevice(cfg config, c sdk.Call, beforeSet map[string]bool, expires int) {
	deadline := time.Now().Add(time.Duration(expires) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		devices, err := maaFetchDevices(reqCtx, cfg)
		cancel()
		if err != nil {
			continue
		}
		var newDevices []maaDevice
		for _, device := range devices {
			if !beforeSet[device.DeviceID] {
				newDevices = append(newDevices, device)
			}
		}
		if len(newDevices) == 0 {
			continue
		}
		store := maaLoadStore(context.Background(), c.State, c.Event.UserID)
		names := make([]string, 0, len(newDevices))
		for _, device := range newDevices {
			maaAddDevice(&store, device.DeviceID)
			if device.DeviceName != "" {
				names = append(names, device.DeviceName)
			} else {
				names = append(names, device.DeviceID)
			}
		}
		if len(store.DeviceIDs) == 1 {
			store.DefaultDeviceID = store.DeviceIDs[0]
		}
		maaSaveStore(context.Background(), c.State, c.Event.UserID, store)
		_ = replyText(context.Background(), c, getMessage("maaend.bind_success", map[string]any{"names": strings.Join(names, "、")}))
		return
	}
}

func handleMaaDevices(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	devices, store, errText := maaUserDevices(ctx, cfg, c.State, c.Event.UserID)
	if errText != "" {
		return replyText(ctx, c, errText)
	}
	if len(devices) == 0 {
		return replyText(ctx, c, getMessage("maaend.no_devices_full"))
	}
	lines := []string{
		getMessage("maaend.device_list_title"),
		getMessage("maaend.device_list_count", map[string]any{"count": len(devices)}),
		"",
	}
	for index, device := range devices {
		defaultMark := ""
		if device.DeviceID == store.DefaultDeviceID {
			defaultMark = getMessage("maaend.device_list_default_mark")
		}
		tasks := strings.Join(device.Capabilities.Tasks, "、")
		if tasks == "" {
			tasks = getMessage("common.placeholder")
		}
		lines = append(lines, getMessage("maaend.device_list_item", map[string]any{
			"index": index + 1, "name": maaDeviceDisplayName(device), "status": maaStatusText(device.Status), "default_mark": defaultMark,
		}))
		version := device.MaaendVersion
		if version == "" {
			version = getMessage("common.placeholder")
		}
		clientVersion := device.ClientVersion
		if clientVersion == "" {
			clientVersion = getMessage("common.placeholder")
		}
		lines = append(lines, getMessage("maaend.device_list_meta", map[string]any{
			"id": device.DeviceID, "maaend_version": version, "client_version": clientVersion,
		}))
		lines = append(lines, getMessage("maaend.device_list_tasks", map[string]any{"tasks": tasks}))
		if device.CurrentJobID != "" {
			lines = append(lines, getMessage("maaend.device_list_current_job", map[string]any{"jobId": device.CurrentJobID}))
		}
		lines = append(lines, "")
	}
	return replyText(ctx, c, strings.TrimSpace(strings.Join(lines, "\n")))
}

func maaDeviceDisplayName(device maaDevice) string {
	if strings.TrimSpace(device.DeviceName) != "" {
		return strings.TrimSpace(device.DeviceName)
	}
	return device.DeviceID
}

func handleMaaSetDevice(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	index := strings.TrimSpace(args)
	if index == "" {
		devices, store, errText := maaUserDevices(ctx, cfg, c.State, c.Event.UserID)
		if errText != "" {
			return replyText(ctx, c, errText)
		}
		if store.DefaultDeviceID == "" {
			return replyText(ctx, c, getMessage("maaend.no_default_device"))
		}
		for position, device := range devices {
			if device.DeviceID == store.DefaultDeviceID {
				return replyText(ctx, c, getMessage("maaend_device.current_default", map[string]any{
					"index": position + 1, "name": maaDeviceDisplayName(device), "status": maaStatusText(device.Status),
				}))
			}
		}
		store.DefaultDeviceID = ""
		maaSaveStore(ctx, c.State, c.Event.UserID, store)
		return replyText(ctx, c, getMessage("maaend.default_device_invalid"))
	}
	device, store, position, errText := maaResolveDevice(ctx, cfg, c.State, c.Event.UserID, index)
	if errText != "" {
		return replyText(ctx, c, errText)
	}
	store.DefaultDeviceID = device.DeviceID
	maaSaveStore(ctx, c.State, c.Event.UserID, store)
	return replyText(ctx, c, getMessage("maaend_device.set_default_ok", map[string]any{
		"index": position, "name": maaDeviceDisplayName(device), "status": maaStatusText(device.Status),
	}))
}

func maaFetchTasks(ctx context.Context, cfg config, deviceID string) (*maaTaskList, string) {
	res, _, err := maaRequest(ctx, cfg, http.MethodGet, "/api/maaend/devices/"+url.PathEscape(deviceID)+"/tasks", nil, false)
	if err != nil || res == nil {
		return nil, getMessage("maaend.task_list_failed")
	}
	if !res.OK || res.Code != 0 {
		if res.Message != "" {
			return nil, res.Message
		}
		return nil, getMessage("maaend.task_list_failed")
	}
	var data maaTaskList
	if err := res.decode(&data); err != nil {
		return nil, getMessage("maaend.task_list_failed")
	}
	return &data, ""
}

func handleMaaTaskList(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	device, _, position, errText := maaResolveDevice(ctx, cfg, c.State, c.Event.UserID, strings.TrimSpace(args))
	if errText != "" {
		return replyText(ctx, c, errText)
	}
	tasks, errText := maaFetchTasks(ctx, cfg, device.DeviceID)
	if errText != "" {
		return replyText(ctx, c, errText)
	}
	return replyText(ctx, c, maaTaskListText(device, position, tasks))
}

func maaTaskListText(device maaDevice, position int, taskList *maaTaskList) string {
	lines := []string{getMessage("maaend.task_list_title", map[string]any{"device": maaDeviceDisplayName(device)})}
	if taskList == nil || len(taskList.Tasks) == 0 {
		lines = append(lines, getMessage("maaend.task_list_empty"))
	} else {
		for index, task := range taskList.Tasks {
			labelText := ""
			if task.Label != "" {
				labelText = getMessage("maaend.task_list_label", map[string]any{"label": task.Label})
			}
			desc := ""
			if task.Description != "" {
				desc = getMessage("maaend.task_list_desc", map[string]any{"desc": task.Description})
			}
			lines = append(lines, getMessage("maaend.task_list_item", map[string]any{
				"index": index + 1, "name": task.Name, "label": labelText, "desc": desc,
			}))
		}
	}
	controllers := ""
	resources := ""
	firstTask := "daily"
	if taskList != nil {
		controllers = strings.Join(taskList.Controllers, "、")
		resources = strings.Join(taskList.Resources, "、")
		if len(taskList.Tasks) > 0 && taskList.Tasks[0].Name != "" {
			firstTask = taskList.Tasks[0].Name
		}
	}
	if controllers == "" {
		controllers = getMessage("common.placeholder")
	}
	if resources == "" {
		resources = getMessage("common.placeholder")
	}
	lines = append(lines, getMessage("maaend.task_list_meta", map[string]any{"controllers": controllers, "resources": resources}))
	lines = append(lines, "", getMessage("maaend.task_list_exec_hint", map[string]any{"task": firstTask}))
	return strings.Join(lines, "\n")
}

func handleMaaExec(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	device, store, position, errText := maaResolveDevice(ctx, cfg, c.State, c.Event.UserID, "")
	if errText != "" {
		return replyText(ctx, c, errText)
	}
	taskList, errText := maaFetchTasks(ctx, cfg, device.DeviceID)
	if errText != "" {
		return replyText(ctx, c, errText)
	}
	taskPart := strings.TrimSpace(args)
	if taskPart == "" {
		return replyText(ctx, c, maaTaskListText(device, position, taskList))
	}

	taskArgs := strings.Fields(taskPart)
	resolved := make([]struct {
		Name  string
		Label string
	}, 0, len(taskArgs))
	for _, arg := range taskArgs {
		if number, err := strconv.Atoi(arg); err == nil && strconv.Itoa(number) == arg {
			if number < 1 || number > len(taskList.Tasks) {
				return replyText(ctx, c, getMessage("maaend.task_index_out", map[string]any{"num": number, "max": len(taskList.Tasks)}))
			}
			task := taskList.Tasks[number-1]
			resolved = append(resolved, struct {
				Name  string
				Label string
			}{Name: task.Name, Label: task.Label})
			continue
		}
		label := ""
		for _, task := range taskList.Tasks {
			if task.Name == arg {
				label = task.Label
				break
			}
		}
		resolved = append(resolved, struct {
			Name  string
			Label string
		}{Name: arg, Label: label})
	}
	if len(resolved) == 0 {
		return replyText(ctx, c, maaTaskListText(device, position, taskList))
	}

	controller := "Win32"
	if len(taskList.Controllers) > 0 && taskList.Controllers[0] != "" {
		controller = taskList.Controllers[0]
	}
	resource := "Official"
	if len(taskList.Resources) > 0 && taskList.Resources[0] != "" {
		resource = taskList.Resources[0]
	}
	tasks := make([]map[string]any, 0, len(resolved))
	for _, task := range resolved {
		tasks = append(tasks, map[string]any{"name": task.Name, "options": map[string]any{}})
	}
	body := map[string]any{"controller": controller, "resource": resource, "tasks": tasks}
	res, _, err := maaRequest(ctx, cfg, http.MethodPost, "/api/maaend/devices/"+url.PathEscape(device.DeviceID)+"/tasks", body, false)
	if err != nil || res == nil {
		return replyText(ctx, c, getMessage("maaend.task_run_failed"))
	}
	if res.Code == 40001 {
		return replyText(ctx, c, getMessage("maaend.device_offline"))
	}
	if res.Code == 40002 {
		message := res.Message
		if message == "" {
			message = getMessage("maaend.device_busy")
		}
		return replyText(ctx, c, message)
	}
	if !res.OK || res.Code != 0 {
		message := res.Message
		if message == "" {
			message = getMessage("maaend.request_failed", map[string]any{"code": res.Code})
		}
		return replyText(ctx, c, message)
	}
	var data struct {
		JobID string `json:"job_id"`
	}
	_ = res.decode(&data)
	if data.JobID != "" {
		maaSaveJobID(&store, data.JobID)
		maaSaveStore(ctx, c.State, c.Event.UserID, store)
	}
	descriptionParts := make([]string, 0, len(resolved))
	for _, task := range resolved {
		if task.Label != "" {
			descriptionParts = append(descriptionParts, task.Label+"("+task.Name+")")
		} else {
			descriptionParts = append(descriptionParts, task.Name)
		}
	}
	message := getMessage("maaend.task_dispatched", map[string]any{"task": strings.Join(descriptionParts, "、")})
	if data.JobID != "" {
		message = strings.Replace(message, "#1", "#"+data.JobID, 1)
	}
	if err := replyText(ctx, c, message); err != nil {
		return err
	}
	maaSendScreenshot(ctx, c, cfg, device.DeviceID)
	if data.JobID != "" {
		go maaPollJob(cfg, c, data.JobID, device.DeviceID)
	}
	return nil
}

func maaSendScreenshot(ctx context.Context, c sdk.Call, cfg config, deviceID string) bool {
	data, imageURL := maaScreenshotData(ctx, cfg, deviceID)
	if len(data) > 0 {
		if err := c.ReplyImageBytes(ctx, data); err == nil {
			return true
		}
	}
	if imageURL != "" {
		if err := c.ReplyImage(ctx, imageURL); err == nil {
			return true
		}
	}
	return false
}

func maaScreenshotData(ctx context.Context, cfg config, deviceID string) ([]byte, string) {
	res, data, err := maaRequest(ctx, cfg, http.MethodGet, "/api/maaend/devices/"+url.PathEscape(deviceID)+"/screenshot", nil, true)
	if err != nil {
		return nil, ""
	}
	if len(data) > 0 {
		return data, ""
	}
	if res == nil || !res.OK || res.Code != 0 {
		return nil, ""
	}
	var payload struct {
		Base64Image string `json:"base64_image"`
		ImageURL    string `json:"image_url"`
		URL         string `json:"url"`
	}
	if err := res.decode(&payload); err != nil {
		return nil, ""
	}
	if payload.Base64Image != "" {
		if decoded, err := decodeBase64(payload.Base64Image); err == nil {
			return decoded, ""
		}
	}
	if payload.ImageURL != "" {
		return nil, payload.ImageURL
	}
	return nil, payload.URL
}

func maaPollJob(cfg config, c sdk.Call, jobID, deviceID string) {
	for attempt := 1; attempt <= 120; attempt++ {
		time.Sleep(15 * time.Second)
		reqCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		res, _, err := maaRequest(reqCtx, cfg, http.MethodGet, "/api/maaend/jobs/"+url.PathEscape(jobID), nil, false)
		cancel()
		if err != nil || res == nil || !res.OK || res.Code != 0 {
			continue
		}
		var job maaJob
		if err := res.decode(&job); err != nil {
			continue
		}
		if job.Status != "completed" && job.Status != "failed" && job.Status != "cancelled" {
			continue
		}
		duration := getMessage("common.placeholder")
		if job.DurationMS > 0 {
			duration = maaFormatDuration(int(mathCeilDiv(job.DurationMS, 1000)))
		}
		lines := []string{
			getMessage("maaend.job_finish_title", map[string]any{"status": maaJobStatusText(job.Status)}),
			getMessage("maaend.job_finish_device", map[string]any{"device": maaJobDeviceName(job)}),
			getMessage("maaend.job_finish_duration", map[string]any{"duration": duration}),
		}
		if job.Error != "" {
			lines = append(lines, getMessage("maaend.job_finish_error", map[string]any{"error": job.Error}))
		}
		_ = replyText(context.Background(), c, strings.Join(lines, "\n"))
		if job.Status == "completed" {
			maaSendScreenshot(context.Background(), c, cfg, deviceID)
		}
		return
	}
}

func mathCeilDiv(value, divisor int64) int64 {
	if divisor <= 0 {
		return value
	}
	return (value + divisor - 1) / divisor
}

func maaJobDeviceName(job maaJob) string {
	if strings.TrimSpace(job.DeviceName) != "" {
		return strings.TrimSpace(job.DeviceName)
	}
	if strings.TrimSpace(job.DeviceID) != "" {
		return strings.TrimSpace(job.DeviceID)
	}
	return getMessage("common.placeholder")
}

func handleMaaStatus(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return replyText(ctx, c, getMessage("maaend.no_api_key"))
	}
	store := maaLoadStore(ctx, c.State, c.Event.UserID)
	input := strings.TrimSpace(args)
	if input == "" {
		input = "1"
	}
	jobID := maaResolveJobID(store, input)
	if jobID == "" {
		return replyText(ctx, c, getMessage("maaend.job_not_found", map[string]any{"id": input}))
	}
	res, _, err := maaRequest(ctx, cfg, http.MethodGet, "/api/maaend/jobs/"+url.PathEscape(jobID), nil, false)
	if err != nil || res == nil {
		return replyText(ctx, c, getMessage("maaend.job_query_failed"))
	}
	if !res.OK || res.Code != 0 {
		message := res.Message
		if message == "" {
			message = getMessage("maaend.query_failed", map[string]any{"code": res.Code})
		}
		return replyText(ctx, c, message)
	}
	var job maaJob
	if err := res.decode(&job); err != nil {
		return replyText(ctx, c, getMessage("maaend.job_query_failed"))
	}
	progress := getMessage("common.placeholder")
	if job.Progress.Total > 0 {
		progress = fmt.Sprintf("%d/%d", job.Progress.Completed, job.Progress.Total)
	}
	duration := getMessage("common.placeholder")
	if job.DurationMS > 0 {
		duration = fmt.Sprintf("%dms", job.DurationMS)
	}
	taskName := job.CurrentTask
	if taskName == "" {
		taskName = getMessage("common.placeholder")
	}
	lines := []string{
		getMessage("maaend.job_status_title"),
		getMessage("maaend.job_status_id", map[string]any{"id": job.JobID}),
		getMessage("maaend.job_status_device", map[string]any{"device": maaJobDeviceName(job)}),
		getMessage("maaend.job_status_status", map[string]any{"status": maaJobStatusText(job.Status)}),
		getMessage("maaend.job_status_task", map[string]any{"task": taskName}),
		getMessage("maaend.job_status_progress", map[string]any{"progress": progress}),
		getMessage("maaend.job_status_duration", map[string]any{"duration": duration}),
	}
	if job.Error != "" {
		lines = append(lines, getMessage("maaend.job_status_error", map[string]any{"error": job.Error}))
	}
	if len(job.Logs) > 0 {
		lines = append(lines, "", getMessage("maaend.job_status_logs"))
		start := len(job.Logs) - 5
		if start < 0 {
			start = 0
		}
		for _, log := range job.Logs[start:] {
			lines = append(lines, getMessage("maaend.job_status_log_line", map[string]any{"level": log.Level, "message": log.Message}))
		}
	}
	return replyText(ctx, c, strings.Join(lines, "\n"))
}

func handleMaaStop(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return replyText(ctx, c, getMessage("maaend.no_api_key"))
	}
	store := maaLoadStore(ctx, c.State, c.Event.UserID)
	input := strings.TrimSpace(args)
	if input == "" {
		input = "1"
	}
	jobID := maaResolveJobID(store, input)
	if jobID == "" {
		return replyText(ctx, c, getMessage("maaend.job_not_found", map[string]any{"id": input}))
	}
	res, _, err := maaRequest(ctx, cfg, http.MethodPost, "/api/maaend/jobs/"+url.PathEscape(jobID)+"/stop", map[string]any{}, false)
	if err != nil || res == nil || !res.OK || res.Code != 0 {
		if res != nil && res.Message != "" {
			return replyText(ctx, c, res.Message)
		}
		return replyText(ctx, c, getMessage("maaend.stop_failed"))
	}
	var data struct {
		Message string `json:"message"`
	}
	_ = res.decode(&data)
	if data.Message == "" {
		data.Message = getMessage("maaend.stop_ok")
	}
	return replyText(ctx, c, data.Message)
}

func handleMaaScreenshot(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	device, _, _, errText := maaResolveDevice(ctx, cfg, c.State, c.Event.UserID, strings.TrimSpace(args))
	if errText != "" {
		return replyText(ctx, c, errText)
	}
	if !maaSendScreenshot(ctx, c, cfg, device.DeviceID) {
		return replyText(ctx, c, getMessage("maaend.screenshot_failed"))
	}
	return nil
}

func handleMaaReset(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	device, _, _, errText := maaResolveDevice(ctx, cfg, c.State, c.Event.UserID, strings.TrimSpace(args))
	if errText != "" {
		return replyText(ctx, c, errText)
	}
	res, _, err := maaRequest(ctx, cfg, http.MethodPost, "/api/maaend/devices/"+url.PathEscape(device.DeviceID)+"/reset", map[string]any{}, false)
	if err != nil || res == nil || !res.OK || res.Code != 0 {
		if res != nil && res.Message != "" {
			return replyText(ctx, c, res.Message)
		}
		return replyText(ctx, c, getMessage("maaend.reset_failed"))
	}
	var data struct {
		Message string `json:"message"`
	}
	_ = res.decode(&data)
	if data.Message == "" {
		data.Message = getMessage("maaend.reset_ok")
	}
	return replyText(ctx, c, data.Message)
}

func handleMaaDelete(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	index := strings.TrimSpace(args)
	if index == "" {
		return replyText(ctx, c, getMessage("maaend.delete_usage"))
	}
	device, store, _, errText := maaResolveDevice(ctx, cfg, c.State, c.Event.UserID, index)
	if errText != "" {
		return replyText(ctx, c, errText)
	}
	res, _, err := maaRequest(ctx, cfg, http.MethodDelete, "/api/maaend/devices/"+url.PathEscape(device.DeviceID), nil, false)
	if err != nil || res == nil || !res.OK || res.Code != 0 {
		if res != nil && res.Message != "" {
			return replyText(ctx, c, res.Message)
		}
		return replyText(ctx, c, getMessage("maaend.delete_failed"))
	}
	maaRemoveDevice(&store, device.DeviceID)
	maaSaveStore(ctx, c.State, c.Event.UserID, store)
	var data struct {
		Message string `json:"message"`
	}
	_ = res.decode(&data)
	if data.Message == "" {
		data.Message = getMessage("maaend.delete_ok")
	}
	return replyText(ctx, c, data.Message)
}

func handleMaaHistory(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return replyText(ctx, c, getMessage("maaend.no_api_key"))
	}
	page := 1
	deviceFilter := ""
	fields := strings.Fields(args)
	if len(fields) > 0 {
		if parsed, err := strconv.Atoi(fields[0]); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if len(fields) > 1 {
		deviceFilter = strings.TrimSpace(fields[1])
	}
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("limit", "10")
	if deviceFilter != "" {
		query.Set("device_id", deviceFilter)
	}
	res, _, err := maaRequest(ctx, cfg, http.MethodGet, "/api/maaend/jobs?"+query.Encode(), nil, false)
	if err != nil || res == nil || !res.OK || res.Code != 0 {
		if res != nil && res.Message != "" {
			return replyText(ctx, c, res.Message)
		}
		return replyText(ctx, c, getMessage("maaend.job_history_failed"))
	}
	var data struct {
		Jobs  []maaJob `json:"jobs"`
		Total int      `json:"total"`
	}
	if err := res.decode(&data); err != nil {
		return replyText(ctx, c, getMessage("maaend.job_history_failed"))
	}
	if len(data.Jobs) == 0 {
		return replyText(ctx, c, getMessage("maaend.no_job_history"))
	}
	lines := []string{getMessage("maaend.job_history_title", map[string]any{"page": page, "total": data.Total}), ""}
	for _, job := range data.Jobs {
		duration := getMessage("common.placeholder")
		if job.DurationMS != 0 {
			duration = strconv.FormatInt(job.DurationMS, 10)
		}
		lines = append(lines, getMessage("maaend.job_history_item", map[string]any{
			"id": job.JobID, "device": maaJobDeviceName(job), "status": maaJobStatusText(job.Status), "duration": duration,
		}))
	}
	return replyText(ctx, c, strings.Join(lines, "\n"))
}
