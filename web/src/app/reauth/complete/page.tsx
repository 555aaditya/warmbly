// Where a "confirm it is you" provider window lands. It hands the result to the
// dashboard tab that opened it and closes; the code is worthless without the
// binding that tab kept.

import React from "react";
import { Link } from "@tanstack/react-router";
import { CheckIcon, XIcon } from "lucide-react";
import { Logo } from "@/components/svg";
import { reauthReturnMessage, relayOAuthReturn } from "@/lib/oauthReturn";

export default function ReauthCompletePage() {
    const [message] = React.useState(() => reauthReturnMessage(window.location.search));
    const [delivered, setDelivered] = React.useState(false);

    React.useEffect(() => {
        // The query carries a single-use code; keep it out of history.
        window.history.replaceState(null, "", window.location.pathname);
        let sent = false;
        try {
            if (window.opener && window.opener !== window) {
                window.opener.postMessage(message, window.location.origin);
                sent = true;
            }
        } catch {
            /* the opener is gone or cross-origin; the channel below still reaches it */
        }
        if (relayOAuthReturn(message)) sent = true;
        if (!sent) return;
        setDelivered(true);
        const t = window.setTimeout(() => window.close(), 400);
        return () => window.clearTimeout(t);
    }, [message]);

    const ok = Boolean(message.code) && !message.error;
    const title = ok ? "Confirmed" : message.cancelled ? "Cancelled" : "Could not confirm";
    return (
        <div className="min-h-dvh flex items-center justify-center bg-slate-50 px-4">
            <div className="w-full max-w-sm rounded-xl border border-slate-200 bg-white p-6 text-center shadow-sm">
                <Logo className="w-7 mx-auto text-slate-900" />
                <span className={`mt-4 mx-auto size-10 rounded-full inline-flex items-center justify-center ${ok ? "bg-emerald-50 text-emerald-600" : "bg-rose-50 text-rose-600"}`}>
                    {ok ? <CheckIcon className="w-5 h-5" /> : <XIcon className="w-5 h-5" />}
                </span>
                <p className="mt-3 text-[14px] font-semibold text-slate-900">{title}</p>
                {message.error && !ok && <p className="mt-1 text-[12.5px] text-rose-600 leading-relaxed">{message.error}</p>}
                <p className="mt-1 text-[12.5px] text-slate-500 leading-relaxed">
                    {!delivered
                        ? "This window could not reach your Warmbly tab. Close it and try again from there."
                        : ok
                            ? "Finishing in your Warmbly tab. This window closes on its own; if it stays open, close it and go back to that tab."
                            : "This window closes on its own. Go back to your Warmbly tab to try again."}
                </p>
                {!delivered && (
                    <Link to="/app" className="mt-4 inline-flex h-8 px-3 items-center rounded-md bg-slate-900 text-white text-[12.5px] font-medium">
                        Back to Warmbly
                    </Link>
                )}
            </div>
        </div>
    );
}
