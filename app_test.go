package main

import (
	"archive/zip"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseTargetsText(t *testing.T) {
	content := "\ufeffhttps://www.baidu.com/path\nboce.com, example.com；http://foo.test"
	want := []string{"www.baidu.com", "boce.com", "example.com", "foo.test"}

	got := parseTargetsText(content)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargetsText() = %#v, want %#v", got, want)
	}
}

func TestParseTargetsTextDeduplicates(t *testing.T) {
	content := "www.baidu.com\nhttps://www.baidu.com/search?q=1\n"
	want := []string{"www.baidu.com"}

	got := parseTargetsText(content)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargetsText() = %#v, want %#v", got, want)
	}
}

func TestSetBoceTaskQuery(t *testing.T) {
	query := url.Values{}
	setBoceTaskQuery(query, "test-key", "baidu.com,boce.com")

	if query.Get("key") != "test-key" || query.Get("host") != "baidu.com,boce.com" || query.Get("from") != "" {
		t.Fatalf("setBoceTaskQuery() = %s", query.Encode())
	}
}

func TestErrorRemarkHidesRequestURL(t *testing.T) {
	err := &url.Error{
		Op:  "Get",
		URL: "https://api.boce.com/v3/task/create/qq?host=www.qq.com&key=secret",
		Err: errors.New("EOF"),
	}

	got := errorRemark(err)
	if got != "请求失败: EOF" {
		t.Fatalf("errorRemark() = %q", got)
	}
}

func TestQQStatusText(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{code: 1, want: "正常"},
		{code: 2, want: "拦截"},
		{code: 3, want: "失败"},
	}

	for _, tt := range tests {
		got := qqStatusText(tt.code, nil)
		if got != tt.want {
			t.Fatalf("qqStatusText(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestWechatStatusText(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{code: 1, want: "正常"},
		{code: 2, want: "拦截"},
		{code: 3, want: "失败"},
	}

	for _, tt := range tests {
		got := wechatStatusText(tt.code, nil)
		if got != tt.want {
			t.Fatalf("wechatStatusText(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestBlacklistStatusText(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{code: 1, want: "正常"},
		{code: 2, want: "黑名单"},
		{code: 3, want: "失败"},
		{code: 9, want: "未知"},
	}

	for _, tt := range tests {
		got := blacklistStatusText(tt.code, nil)
		if got != tt.want {
			t.Fatalf("blacklistStatusText(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestWallStatusText(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{code: 0, want: "正常"},
		{code: 1, want: "被墙"},
		{code: 2, want: "疑似被墙"},
		{code: 3, want: "失败"},
		{code: 4, want: "域名格式错误"},
		{code: 9, want: "未知"},
	}

	for _, tt := range tests {
		got := wallStatusText(tt.code, nil)
		if got != tt.want {
			t.Fatalf("wallStatusText(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestICPStatusText(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{code: 1, want: "已备案"},
		{code: 0, want: "未备案"},
		{code: 9, want: "未知"},
	}

	for _, tt := range tests {
		got := icpStatusText(tt.code, nil)
		if got != tt.want {
			t.Fatalf("icpStatusText(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestSummarizeRowsCountsInterceptAsAbnormal(t *testing.T) {
	rows := []DetectRow{
		{Status: "拦截"},
		{Status: "污染"},
		{Status: "未备案"},
		{Status: "黑名单"},
		{Status: "被墙"},
		{Status: "疑似被墙"},
		{Status: "域名格式错误"},
		{Status: "正常"},
		{Status: "已备案"},
		{Status: "失败"},
	}

	got := summarizeRows(rows)
	if got.Total != 10 || got.Checked != 10 || got.Pollution != 7 || got.Normal != 2 || got.Failed != 1 {
		t.Fatalf("summarizeRows() = %#v", got)
	}
}

func TestWriteRowsXLSX(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.xlsx")
	rows := []DetectRow{
		{
			Type:        "微信拦截",
			Target:      "www.boce.com",
			Status:      "拦截",
			CheckedAt:   "2026-06-12 12:00:00",
			ErrorRemark: "ok & checked",
		},
	}

	if err := writeRowsXLSX(path, rows); err != nil {
		t.Fatalf("writeRowsXLSX() error = %v", err)
	}

	worksheet := readWorksheetXML(t, path)

	if !strings.Contains(worksheet, "微信拦截") || !strings.Contains(worksheet, "ok &amp; checked") {
		t.Fatalf("worksheet content missing exported data: %s", worksheet)
	}
}

func TestWriteRowsXLSXForICP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "icp.xlsx")
	rows := []DetectRow{
		{
			Type:      "备案查询",
			Domain:    "baidu.com",
			Status:    "已备案",
			BeianCode: "京ICP证030173号-1",
			SiteName:  "百度",
		},
	}

	if err := writeRowsXLSX(path, rows); err != nil {
		t.Fatalf("writeRowsXLSX() error = %v", err)
	}

	worksheet := readWorksheetXML(t, path)
	for _, text := range []string{"域名", "是否备案", "备案号", "网站名称", "baidu.com", "京ICP证030173号-1", "百度"} {
		if !strings.Contains(worksheet, text) {
			t.Fatalf("worksheet missing %q: %s", text, worksheet)
		}
	}
}

func TestDefaultExportFilename(t *testing.T) {
	got := defaultExportFilename([]DetectRow{{Type: `QQ拦截/检测`}})
	if !strings.HasPrefix(got, "QQ拦截_检测_") || !strings.HasSuffix(got, ".xlsx") {
		t.Fatalf("defaultExportFilename() = %q", got)
	}
}

func readWorksheetXML(t *testing.T, path string) string {
	t.Helper()

	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader() error = %v", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if file.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("Open worksheet error = %v", err)
		}
		data, err := io.ReadAll(handle)
		if err != nil {
			t.Fatalf("Read worksheet error = %v", err)
		}
		_ = handle.Close()
		return string(data)
	}

	t.Fatalf("worksheet not found")
	return ""
}
