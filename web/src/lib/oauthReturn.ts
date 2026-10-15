// A sign-in window the browser opened without a link back to the dashboard
// (one allowed from a blocked-popup prompt) lands on /oauth-return. That page
// hands the result to the waiting tab over a same-origin BroadcastChannel,
// and the tab replays it as a message from its own origin, so every flow's
// existing origin and state checks decide whether it is theirs.

const CHANNEL = "warmbly-oauth-return";

export type OAuthReturnMessage =
    | { type: "email_oauth_callback"; provider: string; code: string; state: string; error: string }
    | { source: "warmbly-integration-oauth"; code: string; state: string; error: string }
    | { type: "cloud_oauth_callback"; session: string; status: "ok" | "error"; error?: string; message?: string }
    | ReauthReturnMessage;

/** What /reauth/complete reports to the tab waiting on a re-authentication window. */
export interface ReauthReturnMessage {
    type: "warmbly_reauth";
    code: string;
    error: string;
    cancelled: boolean;
}

/** Reads the provider callback's query on /reauth/complete. */
export function reauthReturnMessage(search: string): ReauthReturnMessage {
    const p = new URLSearchParams(search);
    return {
        type: "warmbly_reauth",
        code: p.get("code") ?? "",
        error: p.get("error") ?? "",
        cancelled: p.get("cancelled") === "1",
    };
}

/** The re-authentication result in a window message from this origin, or undefined. */
export function acceptReauthReturn(event: MessageEvent): ReauthReturnMessage | undefined {
    if (event.origin !== window.location.origin) return undefined;
    const data = event.data as Partial<ReauthReturnMessage> | null;
    if (!data || data.type !== "warmbly_reauth") return undefined;
    return {
        type: "warmbly_reauth",
        code: typeof data.code === "string" ? data.code : "",
        error: typeof data.error === "string" ? data.error : "",
        cancelled: data.cancelled === true,
    };
}

/** The message the callback page put in the fragment of /oauth-return, or null when it is not one. */
export function oauthReturnMessage(hash: string): OAuthReturnMessage | null {
    const p = new URLSearchParams(hash.replace(/^#/, ""));
    const code = p.get("code") ?? "";
    const state = p.get("state") ?? "";
    const error = p.get("error") ?? "";
    if (!state) return null;
    switch (p.get("source")) {
        case "mailbox":
            return { type: "email_oauth_callback", provider: p.get("provider") ?? "", code, state, error };
        case "integration":
            return { source: "warmbly-integration-oauth", code, state, error };
        default:
            return null;
    }
}

/** Sends a sign-in result to every dashboard tab; false when the browser has no BroadcastChannel. */
export function relayOAuthReturn(message: OAuthReturnMessage): boolean {
    if (typeof BroadcastChannel === "undefined") return false;
    const ch = new BroadcastChannel(CHANNEL);
    ch.postMessage(message);
    ch.close();
    return true;
}

let listening = false;

/** Replays results relayed by /oauth-return as window messages. Idempotent. */
export function listenForOAuthReturn(): void {
    if (listening || typeof BroadcastChannel === "undefined") return;
    listening = true;
    new BroadcastChannel(CHANNEL).onmessage = (event: MessageEvent) => {
        window.dispatchEvent(new MessageEvent("message", { data: event.data, origin: window.location.origin }));
    };
}
