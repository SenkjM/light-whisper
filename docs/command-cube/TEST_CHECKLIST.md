<!-- SPDX-FileCopyrightText: 2026 SenkjM -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# 命令方块 — 用户验收清单（E2E-ready）

范围：**不含** PLAN 步骤 7（歌词/字幕/人声分离）与 Agent/dah。

## 制品

1. Actions → `Command-Cube withcore` → `command-cube-withcore-windows-app`（含 CUDA R2T2）
2. （可选）`Command-Cube CUDA` → `command-cube-cuda-native`（冗余于 R2T2；用于 CUDA transcribe 替换 / 旧包补库）
3. （可选薄壳）`Command-Cube without` → `command-cube-without-windows-app`
4. Smoke：`command-cube-smoke`（纯 Go mock，快速冒烟）

## Windows withcore（必测）

- [ ] 安装 NSIS；启动后 Connection：backend=go，ready=是，打包口味 withcore
- [ ] Connection：「CUDA R2T2 库」=是（无需再合并 cuda-native）
- [ ] Token 自动填入；Backend Admin 可建/删非 root Token；stats 有计数
- [ ] 工作台：一键安装模型（Qwen3 + R2T2），进度/取消/失败可恢复；无需 Python
- [ ] 切换 Qwen3 / R2T2；听写有字幕；历史页仅前端
- [ ] 壳设置：主题/语言/说话热键/助手热键/麦电平表
- [ ] Connection：云端点（润色/助手/决策/云 ASR）可保存；密钥进 keyring
- [ ] 窗口无边框：最小化/最大化/关闭；各 Tab 可滚动；暗色 select 可读

## CUDA 路径（有 NVIDIA 时）

- [ ] withcore 安装后 Connection 已显示「CUDA R2T2 库=是」（无需按 [CUDA_NATIVE_INSTALL.md](./CUDA_NATIVE_INSTALL.md) 合并，除非换 CUDA transcribe）
- [ ] 工作台：检测到 GPU → 下载/启用 CUDA 运行时（cudart/cublas）；版本不符有确认
- [ ] 启用后听写走 GPU（状态显示 cuda）；禁用回 CPU
- [ ] 设备偏好：auto / cpu / cuda / vulkan（vulkan 仅对 Qwen3 有意义）

## without 薄壳（可选）

- [ ] 安装 without 包；Connection 配置外部管理 URL + Token；不捆绑 lw-engine

## 已知不测 / 延后

- 步骤 7 工具、Agent/dah
- 外层 8s 分段 vs C++ 16s 滚动窗数值对比（决策已文档化：暂保留外层分段）
- Linux/mac 安装包（本切片以 Windows 为主）
