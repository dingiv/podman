#!/usr/bin/env bash
# easytidy-podman deb 一键构建（对齐官方 podman 的发行版 debian/ 打包姿势）。
#
# 依赖：debhelper (>= 13)   [sudo apt install debhelper]
#       easytidy Go 工具链 ~/tools/go（debian/rules 内置，可被环境 GOROOT 覆盖）
#
# 产物：../easytidy-podman_<version>_amd64.deb
# 安装：sudo apt install ../easytidy-podman_*.deb   （自动替换发行版 podman）
set -euo pipefail
cd "$(dirname "$0")"

version=$(dpkg-parsechangelog -S Version)
echo "==> 构建 easytidy-podman ${version}"
dpkg-buildpackage -us -uc -b

mkdir -p bundle
deb="bundle/easytidy-podman_${version}_amd64.deb"
echo "==> 产物：${deb}"
dpkg-deb --info "$deb" | sed -n '1,12p'
