import { useEffect, useState } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SettingsDisclosure, SettingsReveal } from "./SettingsReveal";

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

it("mounts only when opened and releases effects after closing, with hidden controls inert immediately", () => {
  vi.useFakeTimers();
  const dispose = vi.fn();
  function Content() {
    useEffect(() => dispose, []);
    return <input aria-label="Timeout" />;
  }
  const { rerender } = render(<SettingsReveal open={false}><Content /></SettingsReveal>);
  expect(screen.queryByLabelText("Timeout")).toBeNull();
  rerender(<SettingsReveal open><Content /></SettingsReveal>);
  const input = screen.getByRole("textbox", { name: "Timeout" });
  rerender(<SettingsReveal open={false}><Content /></SettingsReveal>);
  expect(screen.queryByRole("textbox", { name: "Timeout" })).toBeNull();
  expect(input.closest("[inert]")).not.toBeNull();
  act(() => vi.advanceTimersByTime(279));
  expect(input).toBeInTheDocument();
  expect(dispose).not.toHaveBeenCalled();
  act(() => vi.advanceTimersByTime(1));
  expect(input).not.toBeInTheDocument();
  expect(dispose).toHaveBeenCalledOnce();
});

it("cancels a pending close when reopened, preserving the field and its draft", () => {
  vi.useFakeTimers();
  const { rerender } = render(<SettingsReveal open><input aria-label="Draft" /></SettingsReveal>);
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "Keep this prompt" } });
  rerender(<SettingsReveal open={false}><input aria-label="Draft" /></SettingsReveal>);
  act(() => vi.advanceTimersByTime(100));
  rerender(<SettingsReveal open><input aria-label="Draft" /></SettingsReveal>);
  act(() => vi.advanceTimersByTime(280));
  expect(screen.getByRole("textbox")).toBe(input);
  expect(input).toHaveValue("Keep this prompt");
  expect(input.closest("[inert]")).toBeNull();
});

describe("SettingsDisclosure", () => {
  it("supports keyboard opening and closing and retains a saved prompt after a full collapse", async () => {
    const user = userEvent.setup();
    function Prompt() {
      const [value, setValue] = useState("");
      return <SettingsDisclosure label="Assistant prompt">
        <textarea aria-label="Prompt" value={value} onChange={(event) => setValue(event.target.value)} />
      </SettingsDisclosure>;
    }
    render(<Prompt />);
    const trigger = screen.getByRole("button", { name: "Assistant prompt" });
    await user.tab();
    await user.keyboard("{Enter}");
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    await user.type(screen.getByRole("textbox"), "Answer in Chinese");
    await user.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("textbox")).toBeNull();
    await waitFor(() => expect(screen.queryByLabelText("Prompt")).toBeNull());
    await user.keyboard(" ");
    expect(screen.getByRole("textbox")).toHaveValue("Answer in Chinese");
  });

  it("skips the closing delay for reduced motion", () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true }));
    const { rerender } = render(<SettingsReveal open><input aria-label="Reduced" /></SettingsReveal>);
    const input = screen.getByRole("textbox");
    rerender(<SettingsReveal open={false}><input aria-label="Reduced" /></SettingsReveal>);
    expect(input).not.toBeInTheDocument();
  });
});
