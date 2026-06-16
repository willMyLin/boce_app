#!/bin/sh
set -eu

cd "$(dirname "$0")"

app=""
for candidate in boce_tool_app_darwin_arm64 boce_tool_app_darwin_amd64; do
	if [ -e "$candidate" ]; then
		app="$candidate"
		break
	fi
done

if [ -z "$app" ]; then
	echo "未找到 boce_tool_app 的 Mac 可执行文件。"
	echo "请确认脚本和 boce_tool_app_darwin_arm64 / boce_tool_app_darwin_amd64 在同一目录。"
	exit 1
fi

if command -v xattr >/dev/null 2>&1; then
	xattr -dr com.apple.quarantine "$app" 2>/dev/null || true
fi

chmod +x "$app"

echo "已处理: $app"
echo "现在可以运行: ./$app"
