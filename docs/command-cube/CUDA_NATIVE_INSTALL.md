<!-- SPDX-FileCopyrightText: 2026 SenkjM -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# CUDA 原生库（命令方块）

**withcore** 安装包已捆绑 **CPU + CUDA** `audiocpp.dll`（`r2t2-native/cpu` 与 `r2t2-native/cuda`）以及 **CUDA** `transcribe-native`（`VARIANT=cuda`，含 `ggml-cuda.dll`，并仍带 `ggml-vulkan.dll` / CPU 后端）与 ORT。  
**cudart / cublas 不进包**（PLAN §5 按需下载或复用系统）。

单独合并 `command-cube-cuda-native` 对 **当前 withcore（CUDA R2T2 + CUDA transcribe）已可选 / 冗余**。该制品仍可用于在不重装 NSIS 的情况下刷新旧安装上的库。

## 制品

| 制品名 | 内容 |
|--------|------|
| `command-cube-withcore-windows-app` | withcore 安装包（含 CPU + CUDA R2T2 + CUDA transcribe；**不含** cudart/cublas） |
| `command-cube-cuda-native` | （可选）`r2t2-native/cuda/` + CUDA `transcribe-native`；**不含** cudart/cublas |

## 体积提示（大约）

| 组件 | 体积 |
|------|------|
| `ggml-cuda.dll` | ~205 MB |
| `transcribe-native/`（CUDA VARIANT 合计） | ~279 MB（相对旧 cpu-vulkan ~80 MB，约 **+198 MB**） |
| cudart / cublas | **不进包**（按需） |

## 用户侧：启用 GPU

1. 安装 **withcore**（Connection 页「CUDA R2T2 库」与「CUDA transcribe 库」均应为「是」）。
2. 工作台 → 启用 CUDA **运行时**（一键下载 cudart/cublas，或复用系统）。
3. 设备偏好可选 `auto` / `cuda` / `vulkan` / `cpu`；引擎优先加载 CUDA 后端（R2T2：`cuda/audiocpp.dll`；Qwen3：transcribe CUDA）。

## （可选）合并独立 CUDA 制品

仅当旧 withcore 未带 CUDA R2T2 / CUDA transcribe，或需刷新库时：

```powershell
# 解压 Actions 上的 command-cube-cuda-native 后：
.\scripts\install_cuda_native_libs.ps1 -Src C:\path\to\extracted -Dest "C:\Program Files\命令方块\resources"
```

或仓库内：

```bash
./scripts/install_cuda_native_libs.sh /path/to/extracted [/path/to/resources]
```

## 开发机

`stage_withcore_tauri_resources.py withcore` 在存在 `src-tauri/resources/r2t2-native/cuda/` 与 `transcribe-native/` 时会打进包。  
CI withcore 会 `fetch_native_deps_windows.py --transcribe cuda`，并构建 + strip R2T2 CUDA；本地可先跑相同命令 + `build_r2t2_runtime.py --backend cuda` + `strip_cuda_runtime_from_r2t2.py`。

## Vulkan

- **Qwen3**：withcore 的 CUDA transcribe 变体仍含 `ggml-vulkan.dll`；设备偏好可选 `vulkan`。
- **R2T2**：仅 `cpu` / `cuda`，无 Vulkan 后端。无单独 Vulkan CI。
