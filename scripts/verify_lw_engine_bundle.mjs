// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only
/**
 * Ensure the default (pure Go) package resources are present before
 * `pnpm tauri build` / `build:distribution`.
 */
import { access, readFile, stat } from "node:fs/promises";
import { constants } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const ROOT = process.cwd();
const RESOURCES = join(ROOT, "src-tauri", "resources");
const TAURI_CONF = join(ROOT, "src-tauri", "tauri.conf.json");

async function mustExist(rel, { executable = false } = {}) {
  const path = join(ROOT, rel);
  try {
    const metadata = await stat(path);
    if (!metadata.isFile() || metadata.size === 0) {
      throw new Error(`${rel} is missing or empty`);
    }
  } catch (error) {
    if (error?.code === "ENOENT") {
      throw new Error(
        `缺少 ${rel}。请先运行 \`./scripts/build_lw_engine_for_bundle.sh windows\`（或 host/all）。`,
      );
    }
    throw error;
  }
  if (executable) {
    try {
      await access(path, constants.R_OK);
    } catch {
      throw new Error(`${rel} is not readable`);
    }
  }
  return path;
}

export async function verifyLwEngineBundle() {
  const conf = JSON.parse(await readFile(TAURI_CONF, "utf8"));
  const resources = conf?.bundle?.resources ?? {};
  if (resources["resources/engine.tar.xz"]) {
    throw new Error(
      "tauri.conf.json still lists resources/engine.tar.xz — remove it for the pure-Go package",
    );
  }
  const without = process.env.VERIFY_WITHOUT === "1";

  for (const key of Object.keys(resources)) {
    if (key.includes("engine.tar") || key.endsWith(".py")) {
      throw new Error(
        `tauri.conf.json must not bundle Python/PyInstaller artifact: ${key}`,
      );
    }
  }

  if (without) {
    if (resources["resources/lw-engine.exe"]) {
      throw new Error(
        "without flavor must not list resources/lw-engine.exe — run stage_withcore_tauri_resources.py without",
      );
    }
    for (const key of Object.keys(resources)) {
      if (key.includes("r2t2-native") || key.includes("transcribe-native") || key.includes("onnxruntime")) {
        throw new Error(`without flavor must not bundle native runtime: ${key}`);
      }
    }
    console.log("without flavor: thin shell resources OK (no lw-engine / native DLLs)");
    return 0;
  }

  if (!resources["resources/lw-engine.exe"]) {
    throw new Error(
      "tauri.conf.json must list resources/lw-engine.exe for the NSIS package",
    );
  }

  await mustExist("src-tauri/resources/lw-engine.exe", { executable: true });
  await mustExist("src-tauri/resources/fireredvad_vad.onnx");
  await mustExist("src-tauri/resources/fireredvad_cmvn.json");

  if (process.env.VERIFY_WITHCORE === "1") {
    await mustExist("src-tauri/resources/onnxruntime.dll");
    await mustExist("src-tauri/resources/transcribe-native/transcribe.dll");
    await mustExist("src-tauri/resources/r2t2-native/cpu/audiocpp.dll");
    console.log("withcore native DLLs present (CPU R2T2 + transcribe + onnxruntime)");
    // CUDA R2T2 is required in withcore CI (VERIFY_WITHCORE=1); bundled stripped.
    await mustExist("src-tauri/resources/r2t2-native/cuda/audiocpp.dll");
    console.log("withcore includes CUDA R2T2 (r2t2-native/cuda/audiocpp.dll; cudart on-demand)");
    // CUDA Qwen3 transcribe VARIANT (ggml-cuda); cudart/cublas still on-demand.
    await mustExist("src-tauri/resources/transcribe-native/ggml-cuda.dll");
    const variant = (
      await readFile(join(ROOT, "src-tauri/resources/transcribe-native/VARIANT.txt"), "utf8")
    ).trim();
    if (variant.toLowerCase() !== "cuda") {
      throw new Error(
        `withcore requires transcribe VARIANT=cuda (got ${JSON.stringify(variant)})`,
      );
    }
    console.log("withcore includes CUDA transcribe (VARIANT=cuda / ggml-cuda.dll; cudart on-demand)");
  }

  const size = (await stat(join(RESOURCES, "lw-engine.exe"))).size;
  console.log(
    `Go 引擎包资源已就绪: lw-engine.exe (${size} bytes); engine.tar.xz 未打包`,
  );
  return size;
}

async function main() {
  await verifyLwEngineBundle();
}

const invokedAsScript =
  process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;
if (invokedAsScript) {
  main().catch((error) => {
    console.error(`错误: ${error.message}`);
    process.exitCode = 1;
  });
}
