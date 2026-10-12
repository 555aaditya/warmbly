import React from "react";

// Escape closes the dialog unless a dropdown or the confirm is on top of it.
export default function useEscapeToClose(requestClose: () => void) {
    React.useEffect(() => {
        const onKey = (ev: KeyboardEvent) => {
            if (ev.key !== "Escape") return;
            if (document.querySelector("[data-floating], [role='alertdialog']")) return;
            ev.preventDefault();
            requestClose();
        };
        document.addEventListener("keydown", onKey);
        return () => document.removeEventListener("keydown", onKey);
    }, [requestClose]);
}
