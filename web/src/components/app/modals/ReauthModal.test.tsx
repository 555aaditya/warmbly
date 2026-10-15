import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type ReauthMethods from "@/lib/api/models/auth/ReauthMethods";
import ReauthModal from "./ReauthModal";

const navigate = vi.fn();
const logout = vi.fn(() => Promise.resolve());
const methods = vi.fn<() => Promise<ReauthMethods>>();
const reauth = vi.fn();
const beginSSO = vi.fn();
const finishSSO = vi.fn();

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));
vi.mock("@/lib/api/hooks/auth/useLogout", () => ({ default: () => ({ mutateAsync: logout, isPending: false }) }));
vi.mock("@/lib/api/client/auth/reauth", () => ({ default: (...a: unknown[]) => reauth(...a) }));
vi.mock("@/lib/api/client/auth/reauth/methods", () => ({ default: () => methods() }));
vi.mock("@/lib/api/client/auth/reauth/passkey", () => ({ beginPasskeyReauth: () => Promise.resolve({ options: { publicKey: {} } }) }));
vi.mock("@/lib/api/client/auth/reauth/sso", () => ({
    beginSSOReauth: (...a: unknown[]) => beginSSO(...a),
    finishSSOReauth: (...a: unknown[]) => finishSSO(...a),
}));
vi.mock("@/lib/passkey", () => ({
    PasskeyCancelled: class extends Error {},
    confirmWithPasskey: vi.fn(),
    passkeySupported: () => true,
}));

const none: ReauthMethods = { password: false, totp: false, passkey: false, providers: [], native_providers: [], valid_for_seconds: 300 };

function ask() {
    const resolve = vi.fn();
    const reject = vi.fn();
    act(() => {
        window.dispatchEvent(new CustomEvent("reauth-required", { detail: { resolve, reject } }));
    });
    return { resolve, reject };
}

describe("ReauthModal", () => {
    beforeEach(() => {
        vi.clearAllMocks();
    });
    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("offers a Google-only account its provider and no password field", async () => {
        methods.mockResolvedValue({ ...none, providers: [{ id: "google", label: "Google" }] });
        render(<ReauthModal />);
        ask();
        expect(await screen.findByRole("button", { name: "Continue with Google" })).toBeInTheDocument();
        expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
    });

    it("confirms through the provider window and retries the waiting request", async () => {
        methods.mockResolvedValue({ ...none, providers: [{ id: "google", label: "Google" }] });
        const popup = { closed: false, location: { href: "about:blank", replace: vi.fn() }, document: { body: { childElementCount: 1 } }, focus: vi.fn(), close: vi.fn() };
        vi.spyOn(window, "open").mockReturnValue(popup as unknown as Window);
        beginSSO.mockResolvedValue({ url: "https://accounts.google.com/x", binding: "kept-here" });
        finishSSO.mockResolvedValue({ valid_for_seconds: 300 });

        render(<ReauthModal />);
        const { resolve, reject } = ask();
        fireEvent.click(await screen.findByRole("button", { name: "Continue with Google" }));
        await waitFor(() => expect(popup.location.replace).toHaveBeenCalledWith("https://accounts.google.com/x"));

        act(() => {
            window.dispatchEvent(new MessageEvent("message", {
                origin: window.location.origin,
                data: { type: "warmbly_reauth", code: "handoff", error: "", cancelled: false },
            }));
        });
        await waitFor(() => expect(resolve).toHaveBeenCalled());
        expect(finishSSO).toHaveBeenCalledWith("handoff", "kept-here");
        expect(reject).not.toHaveBeenCalled();
    });

    it("ignores a result posted from another origin", async () => {
        methods.mockResolvedValue({ ...none, providers: [{ id: "google", label: "Google" }] });
        const popup = { closed: false, location: { href: "about:blank", replace: vi.fn() }, document: { body: { childElementCount: 1 } }, focus: vi.fn(), close: vi.fn() };
        vi.spyOn(window, "open").mockReturnValue(popup as unknown as Window);
        beginSSO.mockResolvedValue({ url: "https://accounts.google.com/x", binding: "b" });

        render(<ReauthModal />);
        const { resolve } = ask();
        fireEvent.click(await screen.findByRole("button", { name: "Continue with Google" }));
        await waitFor(() => expect(popup.location.replace).toHaveBeenCalled());
        act(() => {
            window.dispatchEvent(new MessageEvent("message", {
                origin: "https://evil.example",
                data: { type: "warmbly_reauth", code: "handoff", error: "", cancelled: false },
            }));
        });
        await new Promise((r) => setTimeout(r, 20));
        expect(finishSSO).not.toHaveBeenCalled();
        expect(resolve).not.toHaveBeenCalled();
    });

    it("explains the way out to an account with nothing to confirm with", async () => {
        methods.mockResolvedValue(none);
        render(<ReauthModal />);
        const { reject } = ask();
        expect(await screen.findByText(/no password, passkey or authenticator yet/)).toBeInTheDocument();
        expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Sign in again" }));
        await waitFor(() => expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ to: "/auth/login" })));
        expect(logout).toHaveBeenCalled();
        expect(reject).toHaveBeenCalled();
    });

    it("falls back to both fields when the methods lookup fails", async () => {
        methods.mockRejectedValue(new Error("offline"));
        render(<ReauthModal />);
        ask();
        expect(await screen.findByLabelText("Password")).toBeInTheDocument();
        expect(screen.getByLabelText("Two-factor or recovery code")).toBeInTheDocument();
    });

    it("answers every request that was waiting with one confirmation", async () => {
        methods.mockResolvedValue({ ...none, password: true });
        reauth.mockResolvedValue({ valid_for_seconds: 300 });
        render(<ReauthModal />);
        const first = ask();
        const second = ask();

        fireEvent.change(await screen.findByLabelText("Password"), { target: { value: "hunter22" } });
        fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
        await waitFor(() => expect(first.resolve).toHaveBeenCalled());
        expect(second.resolve).toHaveBeenCalled();
        expect(screen.queryByLabelText("Two-factor or recovery code")).not.toBeInTheDocument();
    });

    it("rejects every waiting request on Escape", async () => {
        methods.mockResolvedValue({ ...none, password: true });
        render(<ReauthModal />);
        const first = ask();
        const second = ask();
        await screen.findByLabelText("Password");
        fireEvent.keyDown(window, { key: "Escape" });
        expect(first.reject).toHaveBeenCalled();
        expect(second.reject).toHaveBeenCalled();
    });
});
