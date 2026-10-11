// The frame the Apollo dialogs share: provider mark, title, close.

import React from "react";
import { motion } from "framer-motion";
import { XIcon } from "lucide-react";

import { cn } from "@/lib/utils";

import ProviderGlyph from "../../_components/ProviderGlyph";

export default function DialogShell({
    provider,
    providerName,
    title,
    onRequestClose,
    wide,
    children,
}: {
    provider: string;
    providerName: string;
    title: string;
    onRequestClose: () => void;
    wide?: boolean;
    children: React.ReactNode;
}) {
    return (
        <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ duration: 0.15 }}
            onMouseDown={onRequestClose}
            className="fixed inset-0 z-[110] flex items-center justify-center bg-slate-900/30 backdrop-blur-[2px] px-2 sm:px-4"
        >
            <motion.div
                role="dialog"
                aria-modal="true"
                aria-label={title}
                initial={{ y: 8, opacity: 0, scale: 0.985 }}
                animate={{ y: 0, opacity: 1, scale: 1 }}
                transition={{ duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
                onMouseDown={(ev) => ev.stopPropagation()}
                className={cn(
                    "w-full rounded-lg bg-white border border-slate-200 shadow-[0_24px_48px_-12px_rgba(15,23,42,0.18),0_8px_16px_-8px_rgba(15,23,42,0.1)] overflow-hidden flex flex-col max-h-[90dvh]",
                    wide ? "max-w-[860px] h-[min(90dvh,640px)]" : "max-w-[520px]",
                )}
            >
                <header className="h-12 px-4 border-b border-slate-200 flex items-center gap-2.5 shrink-0">
                    <ProviderGlyph provider={provider} name={providerName} size={7} />
                    <span className="text-[10px] uppercase tracking-[0.14em] text-slate-400 font-medium">{providerName}</span>
                    <div className="h-4 w-px bg-slate-200" />
                    <span className="text-[12.5px] text-slate-900 font-medium truncate">{title}</span>
                    <button
                        type="button"
                        onClick={onRequestClose}
                        aria-label="Close"
                        className="ml-auto size-7 rounded-md text-slate-500 hover:text-slate-900 hover:bg-slate-100 inline-flex items-center justify-center transition-colors"
                    >
                        <XIcon className="w-3.5 h-3.5" />
                    </button>
                </header>
                {children}
            </motion.div>
        </motion.div>
    );
}

