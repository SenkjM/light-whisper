<!-- SPDX-FileCopyrightText: 2026 SenkjM -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# CUDA 原生库安装路径（命令方块）

默认 **withcore** 安装包只含 **CPU** `audiocpp.dll` + `cpu-vulkan`/`CPU` transcribe + ORT。  
CUDA 推理 DLL **不**进默认 NSIS（体积与 NVIDIA 运行库授权，见 PLAN §5）。

## 制品

| 制品名 | 内容 |
|--------|------|
| `command-cube-withcore-windows-app` | CPU withcore 安装包 |
| `command-cube-cuda-native` | `r2t2-native/cuda/audiocpp.dll`（+ 可选 CUDA `transcribe-native`）；**不含** cudart/cublas |

## 合并进已安装 / 开发树

```powershell
# 解压 Actions 上的 command-cube-cuda-native 后：
.\scripts\install_cuda_native_libs.ps1 -Src C:\path\to\extracted -Dest "C:\Program Files\命令方块\resources"
```

或仓库内：

```bash
./scripts/install_cuda_native_libs.sh /path/to/extracted [/path/to/resources]
```

合并后 Connection 页「CUDA R2T2 库」应为是。再在工作台启用 CUDA **运行时**（一键下载 cudart/cublas，或复用系统）。

## 开发机：打进 withcore 资源再打包

若 `src-tauri/resources/r2t2-native/cuda/` 已存在，`stage_withcore_tauri_resources.py withcore` 会**自动**打进包。  
CI 默认不构建该目录；要本地带 CUDA 库：先装制品再 `package_flavor.sh withcore`。

## Vulkan

- **Qwen3**：withcore 的 `transcribe-native` 为 `cpu-vulkan` 变体；设备偏好可选 `vulkan`。
- **R2T2**：仅 `cpu` / `cuda`，无 Vulkan 后端。无单独 Vulkan CI。
