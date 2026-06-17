# BOCE 域名检测工具 - DNS 污染检测 / QQ 微信拦截 / ICP 备案查询 / 被墙检测

一个基于 Go + Wails + Vue 构建的 BOCE 桌面检测工具，用于批量检测域名状态。API Key 可在 [BOCE 官网](https://www.boce.com) 申请。

## 功能

- DNS 污染检测
- QQ 拦截检测
- 微信拦截检测
- ICP 备案查询
- 备案黑名单检测
- 被墙检测
- TXT 批量导入检测目标
- 按状态筛选检测结果
- 导出当前列表为 Excel

## 界面预览

![BOCE 检测工具界面](https://github.com/user-attachments/assets/7be5ad3c-58b2-4cbd-9ff4-77ae023302a7)

[查看原图](https://github.com/user-attachments/assets/7be5ad3c-58b2-4cbd-9ff4-77ae023302a7)

## 接口说明

当前 BOCE 请求会统一携带以下参数：

```text
key=申请的 API Key
host=检测目标，多个域名用英文逗号分隔
```

已接入的 BOCE API：

```text
/v3/task/create/pollute
/v3/task/create/qq
/v3/task/create/wechat
/v3/task/create/icp
/v3/task/create/blacklist
/v3/task/create/wall
```

## 开发环境

需要先安装：

- Go
- Node.js / npm
- Wails CLI v2

Wails 安装文档：

https://wails.io/zh-Hans/docs/gettingstarted/installation

安装前端依赖：

```bash
cd frontend
npm install
```

回到项目根目录运行开发模式：

```bash
cd ..
wails dev
```

## 测试

运行后端测试：

```bash
go test ./...
```

构建前端：

```bash
cd frontend
npm run build
```

## 构建

在项目根目录执行：

```bash
wails build
```

按平台构建：

```bash
# Apple Silicon Mac: M1 / M2 / M3 / M4
wails build -platform darwin/arm64

# Intel Mac
wails build -platform darwin/amd64

# Windows 64 位
wails build -platform windows/amd64

# Linux 64 位
wails build -platform linux/amd64
```

也可以一次指定多个平台：

```bash
wails build -platform darwin/amd64,darwin/arm64,windows/amd64
```


构建产物默认输出到：

```text
build/bin
```

已压缩好的发布包可在 GitHub Release 下载：

[https://github.com/willMyLin/boce-domain-checker/releases/latest](https://github.com/willMyLin/boce-domain-checker/releases/latest)

发布包对应关系：

| 平台 | 下载 |
| --- | --- |
| Windows 64 位 | [boce_tool_app_windows_amd64.zip](https://github.com/user-attachments/files/29027781/boce_tool_app_windows_amd64.zip) |
| Intel Mac | [boce_tool_app_darwin_amd64.zip](https://github.com/user-attachments/files/29027774/boce_tool_app_darwin_amd64.zip) |
| Apple Silicon Mac | [boce_tool_app_darwin_arm64.zip](https://github.com/user-attachments/files/29027778/boce_tool_app_darwin_arm64.zip) |

Mac 下载 zip 解压后，如提示没有执行权限，可执行：

```bash
chmod +x boce_tool_app_darwin_arm64
./boce_tool_app_darwin_arm64
```

如果 Mac 提示“文件已损坏”或“无法打开”，通常是下载文件被隔离标记导致，可在解压后的文件目录执行：

```bash
xattr -dr com.apple.quarantine boce_tool_app_darwin_arm64
./boce_tool_app_darwin_arm64
```

Intel Mac 请把命令里的文件名替换为：

```text
boce_tool_app_darwin_amd64
```

如果 zip 解压时就提示损坏，请先确认文件大小和校验值是否一致：

```bash
shasum -a 256 boce_tool_app_darwin_arm64.zip
```

校验值见：

```text
build/release/SHA256SUMS.txt
```
## 目录结构

```text
.
├── app.go              # Wails 后端逻辑和 BOCE API 调用
├── app_test.go         # 后端测试
├── frontend/           # Vue 前端
├── build/              # 图标、平台配置和构建产物目录
├── main.go             # 应用入口
├── wails.json          # Wails 配置
└── README.md
```
