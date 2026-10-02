import { act, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import ScreenVisionAuth from "./ScreenVisionAuth";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
afterEach(() => vi.useRealTimers());

it("transitions from API key to OAuth without exposing closing controls and back to a key provider", () => {
  vi.useFakeTimers();
  const props = { provider: "openai", openaiAuthMode: "api_key" as const, loggedIn: true, apiKey: "qa-key", onChange: vi.fn() };
  const { rerender } = render(<ScreenVisionAuth {...props} />);
  const field = screen.getByLabelText("settings.screenVisionApiKey");
  expect(field).toHaveAttribute("type", "password");
  rerender(<ScreenVisionAuth {...props} openaiAuthMode="oauth" />);
  expect(field).toBeInTheDocument();
  expect(field.closest("[inert]")).not.toBeNull();
  expect(screen.queryByRole("button", { name: "settings.showApiKey" })).toBeNull();
  expect(screen.getByText("settings.screenVisionUsesOauth")).toBeInTheDocument();
  act(() => vi.advanceTimersByTime(280));
  expect(field).not.toBeInTheDocument();
  rerender(<ScreenVisionAuth {...props} provider="cerebras" openaiAuthMode="oauth" />);
  expect(screen.getByLabelText("settings.screenVisionApiKey")).toHaveValue("qa-key");
  expect(screen.getByRole("button", { name: "settings.showApiKey" })).toBeInTheDocument();
});

it("retains the xAI OAuth hint and hides key controls", () => {
  render(<ScreenVisionAuth provider="xai" openaiAuthMode="api_key" xaiAuthMode="oauth"
    loggedIn={false} grokLoggedIn apiKey="" onChange={vi.fn()} />);
  expect(screen.getByText("settings.grokBuildOauthConnectedHint")).toBeInTheDocument();
  expect(screen.queryByLabelText("settings.screenVisionApiKey")).toBeNull();
});
