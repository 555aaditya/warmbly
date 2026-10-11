// Holds a just-minted inbound URL in memory for the page the connect flow opens
// next. Taken once and never persisted, because the URL is the credential.

const fresh = new Map<string, string>();

export function stashInboundUrl(connectionId: string, url: string): void {
    fresh.set(connectionId, url);
}

// Cleared after the current render pass, so a StrictMode double render still reads it.
export function takeInboundUrl(connectionId: string): string | null {
    const url = fresh.get(connectionId) ?? null;
    if (url !== null) window.setTimeout(() => fresh.delete(connectionId), 0);
    return url;
}
