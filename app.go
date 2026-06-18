package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	boceBaseURL   = "https://api.boce.com/v3/"
	boceBatchSize = 20
)

// App struct
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) emitDetectStart(total int) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "detect:start", DetectStartEvent{Total: total})
}

func (a *App) emitDetectRow(row DetectRow) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "detect:row", row)
}

type DetectRequest struct {
	APIKey            string   `json:"apiKey"`
	EnablePollution   bool     `json:"enablePollution"`
	EnableHijack      bool     `json:"enableHijack"`
	EnableWechat      bool     `json:"enableWechat"`
	EnableICP         bool     `json:"enableIcp"`
	EnableBlacklist   bool     `json:"enableBlacklist"`
	EnableWall        bool     `json:"enableWall"`
	Concurrency       int      `json:"concurrency"`
	TimeoutSeconds    int      `json:"timeoutSeconds"`
	ImportedTargetCnt int      `json:"importedTargetCount"`
	Targets           []string `json:"targets"`
}

type DetectRow struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	Target      string `json:"target"`
	Status      string `json:"status"`
	CheckedAt   string `json:"checkedAt"`
	ErrorRemark string `json:"errorRemark"`
	Domain      string `json:"domain"`
	BeianCode   string `json:"beianCode"`
	SiteName    string `json:"siteName"`
}

type DetectSummary struct {
	Total        int `json:"total"`
	Checked      int `json:"checked"`
	Pollution    int `json:"pollution"`
	Normal       int `json:"normal"`
	Unregistered int `json:"unregistered"`
	Failed       int `json:"failed"`
}

type DetectResponse struct {
	Rows       []DetectRow   `json:"rows"`
	Summary    DetectSummary `json:"summary"`
	Progress   int           `json:"progress"`
	ExportPath string        `json:"exportPath"`
	Message    string        `json:"message"`
}

type ImportResponse struct {
	Targets  []string `json:"targets"`
	Message  string   `json:"message"`
	Canceled bool     `json:"canceled"`
}

type ExportResponse struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type DetectStartEvent struct {
	Total int `json:"total"`
}

type bocePolluteResponse struct {
	ErrorCode int            `json:"error_code"`
	Error     string         `json:"error"`
	Data      map[string]int `json:"data"`
}

type boceQQResponse struct {
	ErrorCode int            `json:"error_code"`
	Error     string         `json:"error"`
	Data      map[string]int `json:"data"`
}

type boceICPResponse struct {
	ErrorCode int                    `json:"error_code"`
	Error     string                 `json:"error"`
	Data      map[string]boceICPInfo `json:"data"`
}

type boceICPInfo struct {
	CompanyBeianCode string `json:"companyBeianCode"`
	SiteBeianCode    string `json:"siteBeianCode"`
	Domain           string `json:"domain"`
	Name             string `json:"name"`
	Status           int    `json:"status"`
	CompanyName      string `json:"companyName"`
	WebsiteIndex     string `json:"websiteIndex"`
	Type             string `json:"type"`
	VerifyTime       string `json:"verifyTime"`
	UpdateTime       string `json:"updateTime"`
}

// ImportTXT opens a file picker and parses detection targets from a TXT file.
func (a *App) ImportTXT() ImportResponse {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择检测目标TXT文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "TXT 文件 (*.txt)", Pattern: "*.txt"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return ImportResponse{
			Message: fmt.Sprintf("导入失败: %v", err),
		}
	}
	if path == "" {
		return ImportResponse{
			Message:  "已取消导入",
			Canceled: true,
		}
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return ImportResponse{
			Message: fmt.Sprintf("读取文件失败: %v", err),
		}
	}

	targets := parseTargetsText(string(content))
	if len(targets) == 0 {
		return ImportResponse{
			Message: "未从TXT中解析到有效检测目标",
		}
	}
	return ImportResponse{
		Targets: targets,
		Message: fmt.Sprintf("已导入 %d 条检测目标", len(targets)),
	}
}

// StartDetection runs the selected BOCE checks. An empty or placeholder key
// keeps returning mock data for UI testing.
func (a *App) StartDetection(req DetectRequest) DetectResponse {
	req.APIKey = strings.TrimSpace(req.APIKey)
	if !req.EnablePollution && !req.EnableHijack && !req.EnableWechat && !req.EnableICP && !req.EnableBlacklist && !req.EnableWall {
		return DetectResponse{
			Summary:  DetectSummary{},
			Progress: 100,
			Message:  "请至少选择一个检测类型",
		}
	}

	if req.APIKey == "" || strings.Contains(req.APIKey, "*") {
		response := makeMockDetectionResponse(
			fmt.Sprintf("未填写有效 API Key，返回测试数据，并发 %d，超时 %d 秒", req.Concurrency, req.TimeoutSeconds),
			req.EnablePollution,
			req.EnableHijack,
			req.EnableWechat,
			req.EnableICP,
			req.EnableBlacklist,
			req.EnableWall,
		)
		a.emitDetectStart(len(response.Rows))
		for _, row := range response.Rows {
			a.emitDetectRow(row)
		}
		return response
	}

	targets := normalizeTargets(req.Targets)
	if len(targets) == 0 {
		targets = mockTargets()
	}

	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}

	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(targets) {
		concurrency = len(targets)
	}

	if concurrency > 20 {
		concurrency = 20
	}

	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}

	expectedRows := expectedDetectRows(req, len(targets))
	a.emitDetectStart(expectedRows)
	emitRow := a.emitDetectRow
	rows := make([]DetectRow, 0, len(targets)*6)
	if req.EnablePollution {
		rows = append(rows, runPollutionDetection(client, req.APIKey, targets, concurrency, len(rows), emitRow)...)
	}
	if req.EnableHijack {
		rows = append(rows, runQQDetection(client, req.APIKey, targets, concurrency, len(rows), emitRow)...)
	}
	if req.EnableWechat {
		rows = append(rows, runWechatDetection(client, req.APIKey, targets, concurrency, len(rows), emitRow)...)
	}
	if req.EnableICP {
		rows = append(rows, runICPDetection(client, req.APIKey, targets, concurrency, len(rows), emitRow)...)
	}
	if req.EnableBlacklist {
		rows = append(rows, runBlacklistDetection(client, req.APIKey, targets, concurrency, len(rows), emitRow)...)
	}
	if req.EnableWall {
		rows = append(rows, runWallDetection(client, req.APIKey, targets, concurrency, len(rows), emitRow)...)
	}

	summary := summarizeRows(rows)
	message := detectionMessage(req.EnablePollution, req.EnableHijack, req.EnableWechat, req.EnableICP, req.EnableBlacklist, req.EnableWall, concurrency, timeout)

	return DetectResponse{
		Rows:       rows,
		Summary:    summary,
		Progress:   100,
		ExportPath: "C:/Users/xjw/Desktop/全部污染.xlsx",
		Message:    message,
	}
}

// ExportPollutionExcel exports the rows currently shown in the frontend.
func (a *App) ExportPollutionExcel(rows []DetectRow) ExportResponse {
	if len(rows) == 0 {
		return ExportResponse{
			Message: "当前列表没有可导出的数据",
		}
	}

	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出当前列表数据",
		DefaultFilename: defaultExportFilename(rows),
		Filters: []runtime.FileFilter{
			{DisplayName: "Excel 工作簿 (*.xlsx)", Pattern: "*.xlsx"},
		},
	})
	if err != nil {
		return ExportResponse{
			Message: fmt.Sprintf("导出失败: %v", err),
		}
	}
	if path == "" {
		return ExportResponse{
			Message: "已取消导出",
		}
	}
	if filepath.Ext(path) == "" {
		path += ".xlsx"
	}

	if err := writeRowsXLSX(path, rows); err != nil {
		return ExportResponse{
			Message: fmt.Sprintf("导出失败: %v", err),
		}
	}

	return ExportResponse{
		Path:    path,
		Message: fmt.Sprintf("已导出 %d 条当前列表数据", len(rows)),
	}
}

func queryPollution(client *http.Client, apiKey string, host string) (int, error) {
	endpoint, err := url.Parse(boceBaseURL + "task/create/pollute")
	if err != nil {
		return 0, err
	}

	query := endpoint.Query()
	setBoceTaskQuery(query, apiKey, host)
	endpoint.RawQuery = query.Encode()

	resp, err := client.Get(endpoint.String())
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result bocePolluteResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	if result.ErrorCode != 0 {
		if result.Error != "" {
			return 0, errors.New(result.Error)
		}
		return 0, fmt.Errorf("BOCE error_code %d", result.ErrorCode)
	}

	if status, ok := result.Data[host]; ok {
		return status, nil
	}
	for _, status := range result.Data {
		return status, nil
	}

	return 0, fmt.Errorf("响应缺少检测结果")
}

func queryQQBatch(client *http.Client, apiKey string, hosts []string) (map[string]int, error) {
	return queryBlockBatch(client, apiKey, hosts, "task/create/qq")
}

func queryPollutionBatch(client *http.Client, apiKey string, hosts []string) (map[string]int, error) {
	return queryBlockBatch(client, apiKey, hosts, "task/create/pollute")
}

func queryWechatBatch(client *http.Client, apiKey string, hosts []string) (map[string]int, error) {
	return queryBlockBatch(client, apiKey, hosts, "task/create/wechat")
}

func queryBlacklistBatch(client *http.Client, apiKey string, hosts []string) (map[string]int, error) {
	return queryBlockBatch(client, apiKey, hosts, "task/create/blacklist")
}

func queryWallBatch(client *http.Client, apiKey string, hosts []string) (map[string]int, error) {
	return queryBlockBatch(client, apiKey, hosts, "task/create/wall")
}

func queryICP(client *http.Client, apiKey string, host string) (boceICPInfo, error) {
	endpoint, err := url.Parse(boceBaseURL + "task/create/icp")
	if err != nil {
		return boceICPInfo{}, err
	}

	query := endpoint.Query()
	setBoceTaskQuery(query, apiKey, host)
	endpoint.RawQuery = query.Encode()

	resp, err := client.Get(endpoint.String())
	if err != nil {
		return boceICPInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return boceICPInfo{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result boceICPResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return boceICPInfo{}, err
	}
	if result.ErrorCode != 0 {
		if result.Error != "" {
			return boceICPInfo{}, errors.New(result.Error)
		}
		return boceICPInfo{}, fmt.Errorf("BOCE error_code %d", result.ErrorCode)
	}

	if info, ok := result.Data[host]; ok {
		return info, nil
	}
	for _, info := range result.Data {
		return info, nil
	}

	return boceICPInfo{}, fmt.Errorf("响应缺少备案结果")
}

func queryBlockBatch(client *http.Client, apiKey string, hosts []string, taskPath string) (map[string]int, error) {
	endpoint, err := url.Parse(boceBaseURL + taskPath)
	if err != nil {
		return nil, err
	}

	query := endpoint.Query()
	setBoceTaskQuery(query, apiKey, strings.Join(hosts, ","))
	endpoint.RawQuery = query.Encode()

	resp, err := client.Get(endpoint.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result boceQQResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.ErrorCode != 0 {
		if result.Error != "" {
			return nil, errors.New(result.Error)
		}
		return nil, fmt.Errorf("BOCE error_code %d", result.ErrorCode)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("响应缺少检测结果")
	}

	return result.Data, nil
}

func setBoceTaskQuery(query url.Values, apiKey string, host string) {
	query.Set("key", apiKey)
	query.Set("host", host)
}

func runPollutionDetection(client *http.Client, apiKey string, targets []string, concurrency int, idOffset int, emit func(DetectRow)) []DetectRow {
	return runBlockDetection(client, apiKey, targets, concurrency, idOffset, "污染", queryPollutionBatch, polluteStatusText, emit)
}

func runQQDetection(client *http.Client, apiKey string, targets []string, concurrency int, idOffset int, emit func(DetectRow)) []DetectRow {
	return runBlockDetection(client, apiKey, targets, concurrency, idOffset, "QQ拦截", queryQQBatch, blockStatusText, emit)
}

func runWechatDetection(client *http.Client, apiKey string, targets []string, concurrency int, idOffset int, emit func(DetectRow)) []DetectRow {
	return runBlockDetection(client, apiKey, targets, concurrency, idOffset, "微信拦截", queryWechatBatch, blockStatusText, emit)
}

func runBlacklistDetection(client *http.Client, apiKey string, targets []string, concurrency int, idOffset int, emit func(DetectRow)) []DetectRow {
	return runBlockDetection(client, apiKey, targets, concurrency, idOffset, "备案黑名单", queryBlacklistBatch, blacklistStatusText, emit)
}

func runWallDetection(client *http.Client, apiKey string, targets []string, concurrency int, idOffset int, emit func(DetectRow)) []DetectRow {
	return runBlockDetection(client, apiKey, targets, concurrency, idOffset, "被墙检测", queryWallBatch, wallStatusText, emit)
}

func runICPDetection(client *http.Client, apiKey string, targets []string, concurrency int, idOffset int, emit func(DetectRow)) []DetectRow {
	rows := make([]DetectRow, len(targets))
	jobs := make(chan int)
	var wg sync.WaitGroup

	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				target := targets[idx]
				info, err := queryICP(client, apiKey, target)
				domain := firstNonEmpty(info.Domain, target)
				row := DetectRow{
					ID:          idOffset + idx + 1,
					Type:        "备案查询",
					Target:      target,
					Status:      icpStatusText(info.Status, err),
					CheckedAt:   time.Now().Format("2006-01-02 15:04:05"),
					ErrorRemark: errorRemark(err),
					Domain:      domain,
					BeianCode:   firstNonEmpty(info.SiteBeianCode, info.CompanyBeianCode),
					SiteName:    info.Name,
				}
				rows[idx] = row
				if emit != nil {
					emit(row)
				}
			}
		}()
	}

	for idx := range targets {
		jobs <- idx
	}
	close(jobs)
	wg.Wait()

	return rows
}

func runBlockDetection(client *http.Client, apiKey string, targets []string, concurrency int, idOffset int, rowType string, query func(*http.Client, string, []string) (map[string]int, error), statusText func(int, error) string, emit func(DetectRow)) []DetectRow {
	rows := make([]DetectRow, len(targets))
	batches := make([][]int, 0, (len(targets)+boceBatchSize-1)/boceBatchSize)
	for start := 0; start < len(targets); start += boceBatchSize {
		end := start + boceBatchSize
		if end > len(targets) {
			end = len(targets)
		}

		indexes := make([]int, 0, end-start)
		for idx := start; idx < end; idx++ {
			indexes = append(indexes, idx)
		}
		batches = append(batches, indexes)
	}

	if concurrency > len(batches) {
		concurrency = len(batches)
	}

	jobs := make(chan []int)
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for indexes := range jobs {
				hosts := make([]string, 0, len(indexes))
				for _, idx := range indexes {
					hosts = append(hosts, targets[idx])
				}

				result, err := query(client, apiKey, hosts)
				checkedAt := time.Now().Format("2006-01-02 15:04:05")
				for _, idx := range indexes {
					target := targets[idx]
					statusCode, statusErr := lookupHostStatus(result, target)
					if err != nil {
						statusErr = err
					} else if statusCode == 3 {
						statusErr = errors.New("检测失败")
					}
					row := DetectRow{
						ID:          idOffset + idx + 1,
						Type:        rowType,
						Target:      target,
						Status:      statusText(statusCode, statusErr),
						CheckedAt:   checkedAt,
						ErrorRemark: errorRemark(statusErr),
					}
					rows[idx] = row
					if emit != nil {
						emit(row)
					}
				}
			}
		}()
	}

	for _, batch := range batches {
		jobs <- batch
	}
	close(jobs)
	wg.Wait()

	return rows
}

func lookupHostStatus(result map[string]int, host string) (int, error) {
	if status, ok := result[host]; ok {
		return status, nil
	}
	if status, ok := result["www."+host]; ok {
		return status, nil
	}
	trimmed := strings.TrimPrefix(host, "www.")
	if status, ok := result[trimmed]; ok {
		return status, nil
	}

	return 0, fmt.Errorf("响应缺少 %s 的检测结果", host)
}

func polluteStatusText(statusCode int, err error) string {
	if err != nil {
		return "失败"
	}

	switch statusCode {
	case -1:
		return "未注册"
	case 0:
		return "正常"
	case 1:
		return "污染"
	default:
		return "未知"
	}
}

func qqStatusText(statusCode int, err error) string {
	return blockStatusText(statusCode, err)
}

func wechatStatusText(statusCode int, err error) string {
	return blockStatusText(statusCode, err)
}

func icpStatusText(statusCode int, err error) string {
	if err != nil {
		return "失败"
	}

	switch statusCode {
	case 1:
		return "已备案"
	case 0:
		return "未备案"
	default:
		return "未知"
	}
}

func blockStatusText(statusCode int, err error) string {
	if err != nil {
		return "失败"
	}

	switch statusCode {
	case 1:
		return "正常"
	case 2:
		return "拦截"
	case 3:
		return "失败"
	default:
		return "未知"
	}
}

func blacklistStatusText(statusCode int, err error) string {
	if err != nil {
		return "失败"
	}

	switch statusCode {
	case 1:
		return "正常"
	case 2:
		return "黑名单"
	case 3:
		return "失败"
	default:
		return "未知"
	}
}

func wallStatusText(statusCode int, err error) string {
	if err != nil {
		return "失败"
	}

	switch statusCode {
	case 0:
		return "正常"
	case 1:
		return "被墙"
	case 2:
		return "疑似被墙"
	case 3:
		return "失败"
	case 4:
		return "域名格式错误"
	default:
		return "未知"
	}
}

func detectionMessage(enablePollution bool, enableQQ bool, enableWechat bool, enableICP bool, enableBlacklist bool, enableWall bool, concurrency int, timeout int) string {
	checks := make([]string, 0, 6)
	if enablePollution {
		checks = append(checks, "污染检测")
	}
	if enableQQ {
		checks = append(checks, "QQ拦截检测")
	}
	if enableWechat {
		checks = append(checks, "微信拦截检测")
	}
	if enableICP {
		checks = append(checks, "备案查询")
	}
	if enableBlacklist {
		checks = append(checks, "备案黑名单检测")
	}
	if enableWall {
		checks = append(checks, "被墙检测")
	}

	return fmt.Sprintf("%s完成，并发 %d，超时 %d 秒", strings.Join(checks, "、"), concurrency, timeout)
}

func errorRemark(err error) string {
	if err == nil {
		return ""
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Sprintf("请求失败: %v", urlErr.Err)
	}
	return err.Error()
}

func expectedDetectRows(req DetectRequest, targetCount int) int {
	checks := 0
	if req.EnablePollution {
		checks++
	}
	if req.EnableHijack {
		checks++
	}
	if req.EnableWechat {
		checks++
	}
	if req.EnableICP {
		checks++
	}
	if req.EnableBlacklist {
		checks++
	}
	if req.EnableWall {
		checks++
	}
	return checks * targetCount
}

func summarizeRows(rows []DetectRow) DetectSummary {
	summary := DetectSummary{
		Total:   len(rows),
		Checked: len(rows),
	}

	for _, row := range rows {
		switch row.Status {
		case "污染", "拦截", "未备案", "黑名单", "被墙", "疑似被墙", "域名格式错误":
			summary.Pollution++
		case "正常", "已备案":
			summary.Normal++
		case "未注册":
			summary.Unregistered++
		case "失败":
			summary.Failed++
		}
	}

	return summary
}

func makeMockDetectionResponse(message string, enablePollution bool, enableQQ bool, enableWechat bool, enableICP bool, enableBlacklist bool, enableWall bool) DetectResponse {
	rows := makeMockRows(enablePollution, enableQQ, enableWechat, enableICP, enableBlacklist, enableWall)
	if enablePollution && !enableQQ && !enableWechat && !enableICP && !enableBlacklist && !enableWall {
		total := 93874
		return DetectResponse{
			Rows: rows,
			Summary: DetectSummary{
				Total:        total,
				Checked:      total,
				Pollution:    1498,
				Normal:       92155,
				Unregistered: 203,
				Failed:       18,
			},
			Progress:   100,
			ExportPath: "C:/Users/xjw/Desktop/全部污染.xlsx",
			Message:    message,
		}
	}

	summary := summarizeRows(rows)
	return DetectResponse{
		Rows:       rows,
		Summary:    summary,
		Progress:   100,
		ExportPath: "C:/Users/xjw/Desktop/全部污染.xlsx",
		Message:    message,
	}
}

func makeMockRows(enablePollution bool, enableQQ bool, enableWechat bool, enableICP bool, enableBlacklist bool, enableWall bool) []DetectRow {
	targets := mockTargets()
	base := time.Date(2026, 6, 9, 8, 39, 40, 0, time.Local)
	rows := make([]DetectRow, 0, len(targets)*6)

	if enablePollution {
		for idx, target := range targets {
			rows = append(rows, DetectRow{
				ID:        len(rows) + 1,
				Type:      "污染",
				Target:    target,
				Status:    "正常",
				CheckedAt: base.Add(time.Duration(idx/2) * time.Second).Format("2006-01-02 15:04:05"),
			})
		}
	}

	if enableQQ {
		for idx, target := range targets {
			status := "正常"
			if idx%9 == 0 {
				status = "拦截"
			}
			rows = append(rows, DetectRow{
				ID:        len(rows) + 1,
				Type:      "QQ拦截",
				Target:    target,
				Status:    status,
				CheckedAt: base.Add(time.Duration(idx/2) * time.Second).Format("2006-01-02 15:04:05"),
			})
		}
	}

	if enableWechat {
		for idx, target := range targets {
			status := "正常"
			if idx%7 == 0 {
				status = "拦截"
			}
			rows = append(rows, DetectRow{
				ID:        len(rows) + 1,
				Type:      "微信拦截",
				Target:    target,
				Status:    status,
				CheckedAt: base.Add(time.Duration(idx/2) * time.Second).Format("2006-01-02 15:04:05"),
			})
		}
	}

	if enableICP {
		for idx, target := range targets {
			status := "已备案"
			if idx%8 == 0 {
				status = "未备案"
			}
			rows = append(rows, DetectRow{
				ID:        len(rows) + 1,
				Type:      "备案查询",
				Target:    target,
				Status:    status,
				CheckedAt: base.Add(time.Duration(idx/2) * time.Second).Format("2006-01-02 15:04:05"),
				Domain:    target,
				BeianCode: fmt.Sprintf("测试ICP备案号-%d", idx+1),
				SiteName:  fmt.Sprintf("测试网站%d", idx+1),
			})
		}
	}

	if enableBlacklist {
		for idx, target := range targets {
			status := "正常"
			if idx%10 == 0 {
				status = "黑名单"
			}
			rows = append(rows, DetectRow{
				ID:        len(rows) + 1,
				Type:      "备案黑名单",
				Target:    target,
				Status:    status,
				CheckedAt: base.Add(time.Duration(idx/2) * time.Second).Format("2006-01-02 15:04:05"),
			})
		}
	}

	if enableWall {
		for idx, target := range targets {
			status := "正常"
			if idx%11 == 0 {
				status = "被墙"
			} else if idx%7 == 0 {
				status = "疑似被墙"
			}
			rows = append(rows, DetectRow{
				ID:        len(rows) + 1,
				Type:      "被墙检测",
				Target:    target,
				Status:    status,
				CheckedAt: base.Add(time.Duration(idx/2) * time.Second).Format("2006-01-02 15:04:05"),
			})
		}
	}

	return rows
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func mockTargets() []string {
	return []string{
		"80823456.com",
		"8088w.com",
		"80906.vip",
		"8090xiaoyaoju.com",
		"80969t.com",
		"8099r.com",
		"80gu.com",
		"80suncity.com",
		"80ych.top",
		"810180.com",
		"810461.com",
		"810msc.com",
		"8111777.com",
		"811k3.com",
		"8156d.com",
		"815749.org",
		"816002.com",
		"817890.com",
		"8181138.com",
		"818428.com",
		"81850zq.com",
		"81852.cc",
		"818737.cc",
		"8188q.com",
		"818988c.com",
		"818yule.net",
		"81966.com",
		"81999.cc",
		"820003.com",
		"82023.net",
		"821138.com",
		"82226.vip",
	}
}

func normalizeTargets(targets []string) []string {
	seen := make(map[string]struct{}, len(targets))
	cleaned := make([]string, 0, len(targets))

	for _, target := range targets {
		target = strings.TrimSpace(target)
		target = strings.TrimPrefix(target, "http://")
		target = strings.TrimPrefix(target, "https://")
		target = strings.Trim(target, "/")
		if slashIdx := strings.Index(target, "/"); slashIdx >= 0 {
			target = target[:slashIdx]
		}
		if target == "" {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		cleaned = append(cleaned, target)
	}

	return cleaned
}

func parseTargetsText(content string) []string {
	content = strings.TrimPrefix(content, "\ufeff")
	parts := strings.FieldsFunc(content, func(r rune) bool {
		switch r {
		case '\n', '\r', '\t', ' ', ',', ';', '，', '；':
			return true
		default:
			return false
		}
	})

	for idx, part := range parts {
		parts[idx] = strings.Trim(part, `"'`)
	}

	return normalizeTargets(parts)
}

func writeRowsXLSX(path string, rows []DetectRow) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	zipWriter := zip.NewWriter(file)
	defer zipWriter.Close()

	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets>
    <sheet name="检测结果" sheetId="1" r:id="rId1"/>
  </sheets>
</workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>`,
		"xl/worksheets/sheet1.xml": buildWorksheetXML(rows),
	}

	order := []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"xl/workbook.xml",
		"xl/_rels/workbook.xml.rels",
		"xl/worksheets/sheet1.xml",
	}
	for _, name := range order {
		if err := addZipString(zipWriter, name, files[name]); err != nil {
			return err
		}
	}

	return nil
}

func defaultExportFilename(rows []DetectRow) string {
	detectType := "检测结果"
	if len(rows) > 0 && strings.TrimSpace(rows[0].Type) != "" {
		detectType = strings.TrimSpace(rows[0].Type)
	}
	return fmt.Sprintf("%s_%s.xlsx", sanitizeFilenamePart(detectType), time.Now().Format("20060102"))
}

func sanitizeFilenamePart(value string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		`"`, "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	value = strings.TrimSpace(replacer.Replace(value))
	if value == "" {
		return "检测结果"
	}
	return value
}

func addZipString(zipWriter *zip.Writer, name string, content string) error {
	writer, err := zipWriter.Create(name)
	if err != nil {
		return err
	}
	_, err = io.WriteString(writer, content)
	return err
}

func buildWorksheetXML(rows []DetectRow) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	builder.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	if isICPRows(rows) {
		writeXLSXRow(&builder, 1, []string{"域名", "是否备案", "备案号", "网站名称"})
		for idx, row := range rows {
			writeXLSXRow(&builder, idx+2, []string{
				firstNonEmpty(row.Domain, row.Target),
				row.Status,
				row.BeianCode,
				row.SiteName,
			})
		}
		builder.WriteString(`</sheetData></worksheet>`)
		return builder.String()
	}

	writeXLSXRow(&builder, 1, []string{"类型", "检测目标", "状态", "检测时间", "错误 / 备注"})
	for idx, row := range rows {
		writeXLSXRow(&builder, idx+2, []string{
			row.Type,
			row.Target,
			row.Status,
			row.CheckedAt,
			row.ErrorRemark,
		})
	}
	builder.WriteString(`</sheetData></worksheet>`)
	return builder.String()
}

func isICPRows(rows []DetectRow) bool {
	return len(rows) > 0 && rows[0].Type == "备案查询"
}

func writeXLSXRow(builder *strings.Builder, rowNum int, values []string) {
	builder.WriteString(fmt.Sprintf(`<row r="%d">`, rowNum))
	for idx, value := range values {
		cellRef := fmt.Sprintf("%s%d", xlsxColumnName(idx), rowNum)
		builder.WriteString(fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t>`, cellRef))
		escapeXML(builder, value)
		builder.WriteString(`</t></is></c>`)
	}
	builder.WriteString(`</row>`)
}

func xlsxColumnName(idx int) string {
	name := ""
	for idx >= 0 {
		name = string(rune('A'+idx%26)) + name
		idx = idx/26 - 1
	}
	return name
}

func escapeXML(builder *strings.Builder, value string) {
	if value == "" {
		return
	}
	_ = xml.EscapeText(builder, []byte(value))
}
