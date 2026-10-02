import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import TranscriptionResult from "@/components/TranscriptionResult";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, vars?: Record<string, unknown>) => {
      if (key === "result.title") return "Result";
      if (key === "result.stats") {
        return `${vars?.chars} chars · ${vars?.duration}s · ${vars?.cpm} chars/min`;
      }
      if (key === "result.latency.asr") return `ASR ${vars?.ms}ms`;
      if (key === "result.latency.ai") return `AI ${vars?.ms}ms`;
      if (key === "result.latency.total") return `total ${vars?.ms}ms`;
      return key;
    },
  }),
}));

describe("TranscriptionResult", () => {
  it("shows recognition activity over an existing result on subsequent recordings", () => {
    const { container, rerender } = render(
      <TranscriptionResult text="previous result" originalText="previous result" isProcessing copiedId={null} onCopy={vi.fn()} durationSec={1} charCount={15} />,
    );
    expect(container.querySelector(".result-card")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByText("result.recognizingSpeech")).toBeInTheDocument();
    expect(screen.getByLabelText("result.editableTranscription")).toHaveValue("previous result");
    rerender(<TranscriptionResult text="next result" originalText="next result" isProcessing={false} copiedId={null} onCopy={vi.fn()} durationSec={1} charCount={11} />);
    expect(container.querySelector(".result-card")).toHaveAttribute("aria-busy", "false");
    expect(screen.queryByText("result.recognizingSpeech")).not.toBeInTheDocument();
    expect(screen.getByLabelText("result.editableTranscription")).toHaveValue("next result");
  });
  it("shows ASR, AI polish, and total latency when timing is available", () => {
    render(
      <TranscriptionResult
        text="hello"
        originalText="hello"
        isProcessing={false}
        copiedId={null}
        onCopy={vi.fn()}
        durationSec={1.2}
        charCount={5}
        timing={{ asrMs: 42, polishMs: 900, totalMs: 948 }}
      />,
    );

    expect(screen.getByText(/ASR 42ms/)).toBeInTheDocument();
    expect(screen.getByText(/AI 900ms/)).toBeInTheDocument();
    expect(screen.getByText(/total 948ms/)).toBeInTheDocument();
  });

  it("keeps the final result editable and reports corrections on blur", () => {
    const onTextChange = vi.fn();
    render(
      <TranscriptionResult
        text="original text"
        originalText="original text"
        isProcessing={false}
        copiedId={null}
        onCopy={vi.fn()}
        onTextChange={onTextChange}
        durationSec={null}
        charCount={null}
      />,
    );

    const result = screen.getByLabelText("result.editableTranscription");
    expect(result).not.toHaveAttribute("readonly");
    fireEvent.change(result, { target: { value: "corrected text" } });
    fireEvent.blur(result);
    expect(onTextChange).toHaveBeenCalledWith("corrected text");
  });
});
