<!-- SPDX-FileCopyrightText: 2026 SenkjM -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# CUDA 原生库（命令方块）

**withcore** 安装包已捆绑 **CPU + CUDA** `audiocpp.dll`（`r2t2-native/cpu` 与 `r2t2-native/cuda`）以及 `cpu-vulkan` transcribe + ORT。  
**cudart / cublas 不进包**（PLAN §5 按需下载或复用系统）。

单独合并 `command-cube-cuda-native` 对 **R2T2 GPU 路径已可选 / 冗余**（新 withcore 已含 CUDA R2T2）。该制品仍可用于：把 withcore 的 cpu-vulkan transcribe **换成 CUDA VARIANT**，或在不重装 NSIS 的情况下刷新旧安装上的库。

## 制品

| 制品名 | 内容 |
|--------|------|
| `command-cube-withcore-windows-app` | withcore 安装包（含 CPU + CUDA R2T2；**不含** cudart/cublas） |
| `command-cube-cuda-native` | （可选）`r2t2-native/cuda/` + CUDA `transcribe-native`；**不含** cudart/cublas |

## 用户侧：启用 GPU

1. 安装 **withcore**（Connection 页「CUDA R2T2 库」应为「是」）。
2. 工作台 → 启用 CUDA **运行时**（一键下载 cudart/cublas，或复用系统）。
3. 设备偏好可选 `auto` / `cuda`；引擎优先加载 `cuda/audiocpp.dll`。

## （可选）合并独立 CUDA 制品

仅当需要 CUDA transcribe VARIANT，或旧 withcore 未带 CUDA R2T2 时：

```powershell
# 解压 Actions 上的 command-cube-cuda-native 后：
.\scripts\install_cuda_native_libs.ps1 -Src C:\path\to\extracted -Dest "C:\Program Files\命令方块\resources"
```

或仓库内：

```bash
./scripts/install_cuda_native_libs.sh /path/to/extracted [/path/to/resources]
```

合并 CUDA transcribe 会**覆盖** withcore 自带的 cpu-vulkan `transcribe-native`。

## 开发机

`stage_withcore_tauri_resources.py withcore` 在存在 `src-tauri/resources/r2t2-native/cuda/` 时会打进包。  
CI withcore 会构建并 strip 该目录；本地可先跑 `build_r2t2_runtime.py --backend cuda` + `strip_cuda_runtime_from_r2t2.py`。

## Vulkan

- **Qwen3**：withcore 的 `transcribe-native` 为 `cpu-vulkan` 变体；设备偏好可选 `vulkan`。
- **R2T2**：仅 `cpu` / `cuda`，无 Vulkan 后端。无单独 Vulkan CI。
