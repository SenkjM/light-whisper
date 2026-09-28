import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useModelDiscovery } from "../useModelDiscovery";

const translate = (key: string) => key;
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: translate }) }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
const payload = (id: string) => ({ models: [{ id }], sourceUrl: `https://${id}.example/models` });
type Payload = ReturnType<typeof payload>;

afterEach(() => { vi.useRealTimers(); });

function setup() {
  vi.useFakeTimers();
  const fetchPolishModels = vi.fn<() => Promise<Payload>>().mockResolvedValue(payload("initial"));
  const fetchAssistantModels = vi.fn<() => Promise<Payload>>().mockResolvedValue(payload("assistant"));
  const options = {
    polishModelsContext: "A", polishHasAuth: true, polishMissingAuthMessage: "missing auth",
    fetchPolishModels, assistantModelsContext: "assistant-A", assistantHasAuth: true,
    assistantUseSeparateModel: true, assistantSharesPolishModels: false, fetchAssistantModels,
  };
  const hook = renderHook((props) => useModelDiscovery(props), { initialProps: options });
  return { ...hook, options, fetchPolishModels, fetchAssistantModels };
}

describe("model discovery publication contracts", () => {
  it("keeps the newer catalog when an older fetch completes later", async () => {
    const hook = setup();
    const old = deferred<Payload>();
    hook.fetchPolishModels.mockReturnValueOnce(old.promise);
    let pending!: Promise<void>;
    act(() => { pending = hook.result.current.refreshAiModelsNow(); });
    hook.fetchPolishModels.mockResolvedValueOnce(payload("new"));
    await act(async () => { await hook.result.current.refreshAiModelsNow(); });
    await act(async () => { old.resolve(payload("old")); await pending; });
    expect(hook.result.current.aiModels.map((model) => model.id)).toEqual(["new"]);
    expect(hook.result.current.aiModelsSourceUrl).toContain("new.example");
    expect(hook.result.current.aiModelsLoading).toBe(false);
  });

  it("invalidates old provider and auth responses before publication", async () => {
    const hook = setup();
    const old = deferred<Payload>();
    hook.fetchPolishModels.mockReturnValueOnce(old.promise);
    let pending!: Promise<void>;
    act(() => { pending = hook.result.current.refreshAiModelsNow(); });
    hook.rerender({ ...hook.options, polishModelsContext: "B", polishHasAuth: false });
    await act(async () => { old.resolve(payload("old-A")); await pending; });
    expect(hook.result.current.aiModels).toEqual([]);
    expect(hook.result.current.aiModelsSourceUrl).toBe("");
    expect(hook.result.current.aiModelsLoading).toBe(false);
  });

  it("retains a same-provider catalog on refresh failure and clears it on context change", async () => {
    const hook = setup();
    await act(async () => { await hook.result.current.refreshAiModelsNow(); });
    hook.fetchPolishModels.mockRejectedValueOnce(new Error("offline"));
    await act(async () => { await hook.result.current.refreshAiModelsNow(); });
    expect(hook.result.current.aiModels.map((model) => model.id)).toEqual(["initial"]);
    expect(hook.result.current.aiModelsError).toBe("offline");
    hook.rerender({ ...hook.options, polishModelsContext: "B" });
    expect(hook.result.current.aiModels).toEqual([]);
  });

  it("cancels queued discovery on unmount", async () => {
    const hook = setup();
    hook.unmount();
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(hook.fetchPolishModels).not.toHaveBeenCalled();
    expect(hook.fetchAssistantModels).not.toHaveBeenCalled();
  });
});
