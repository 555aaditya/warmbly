// Apollo page: imports from saved lists, enrichment runs, and the API key.

"use client";

import { Link, useParams } from "@tanstack/react-router";
import { motion } from "framer-motion";
import {
    AlertTriangleIcon,
    ArrowLeftIcon,
    DatabaseZapIcon,
    DownloadCloudIcon,
    ExternalLinkIcon,
    KeyRoundIcon,
    Loader2Icon,
    RefreshCwIcon,
    type LucideIcon,
} from "lucide-react";

import ScrollStrip from "@/components/ui/scroll-strip";
import ResourceViewers from "@/components/app/presence/ResourceViewers";
import { useSearchParams } from "@/hooks/useSearchParams";
import { usePresenceResource } from "@/hooks/PresenceProvider";
import {
    useApolloEnrichments,
    useApolloImportSources,
    useApolloOverview,
} from "@/lib/api/hooks/app/integrations/useApollo";
import { cn } from "@/lib/utils";

import ProviderGlyph from "../../_components/ProviderGlyph";
import StatusPill, { HealthDot } from "../../_components/StatusPill";
import { secondaryBtn } from "../../salesforce/_components/shared";
import { errMsg } from "../../salesforce/_components/util";
import EnrichTab from "../_components/EnrichTab";
import ImportTab from "../_components/ImportTab";
import KeyTab from "../_components/KeyTab";

type TabId = "imports" | "enrich" | "key";

const TABS: { id: TabId; label: string; icon: LucideIcon }[] = [
    { id: "imports", label: "Imports", icon: DownloadCloudIcon },
    { id: "enrich", label: "Enrichment", icon: DatabaseZapIcon },
    { id: "key", label: "API key", icon: KeyRoundIcon },
];

const isTab = (v: string | null): v is TabId => !!v && TABS.some((t) => t.id === v);

export default function ApolloPage() {
    const { id } = useParams({ from: "/app/integrations/apollo/$id" });
    const [params, setParams] = useSearchParams();
    const tab: TabId = isTab(params.get("tab")) ? (params.get("tab") as TabId) : "imports";

    const overview = useApolloOverview(id);
    const sources = useApolloImportSources(id);
    const runs = useApolloEnrichments(id);

    usePresenceResource(id ? `integration_connection:${id}` : null, "viewing");

    function setTab(t: TabId) {
        const next = new URLSearchParams();
        if (t !== "imports") next.set("tab", t);
        setParams(next, { replace: true });
    }

    const ov = overview.data;
    const needsKey = ov?.status === "reauth_required";
    const importing = (sources.data ?? []).some((s) => s.status === "running");
    const enriching = (runs.data ?? []).some((r) => r.status === "queued" || r.status === "running");

    if (overview.isError && !ov) {
        return (
            <div className="flex flex-col min-h-full bg-white">
                <div className="px-3 sm:px-5 pt-3 sm:pt-4 pb-3">
                    <BackLink />
                    <div className="mt-6 rounded-md border border-rose-200 bg-rose-50 px-4 py-3 max-w-xl">
                        <p className="text-[12.5px] font-medium text-rose-800">This Apollo connection could not be loaded</p>
                        <p className="text-[11.5px] text-rose-700 mt-0.5">{errMsg(overview.error, "It may have been disconnected.")}</p>
                        <button type="button" onClick={() => void overview.refetch()} className={cn(secondaryBtn, "mt-2.5")}>
                            <RefreshCwIcon className="w-3 h-3" />
                            Try again
                        </button>
                    </div>
                </div>
            </div>
        );
    }

    return (
        <div className="flex flex-col min-h-full bg-white">
            <div className="px-3 sm:px-5 pt-3 sm:pt-4 pb-3 flex flex-wrap items-start gap-3">
                <div className="min-w-0 flex-1">
                    <BackLink />
                    <div className="flex items-center gap-2.5 min-w-0">
                        <ProviderGlyph provider="apollo" name="Apollo" />
                        <div className="min-w-0">
                            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                                <h1 className="min-w-0 max-w-full text-[18px] font-semibold text-slate-900 truncate">
                                    {ov?.label || "Apollo"}
                                </h1>
                                {ov && <StatusPill status={ov.status} />}
                                <ResourceViewers resource={id ? `integration_connection:${id}` : null} className="shrink-0" />
                            </div>
                            <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11.5px] text-slate-500 mt-0.5">
                                {(ov?.account_name || ov?.account_email) && (
                                    <span className="truncate">{ov.account_name || ov.account_email}</span>
                                )}
                                {ov && (
                                    <span className="inline-flex items-center gap-1" title={ov.health_detail || undefined}>
                                        <HealthDot health={ov.health} />
                                        {ov.health}
                                    </span>
                                )}
                                {ov && (
                                    <span>
                                        {ov.total_imported.toLocaleString()} imported from {ov.sources}{" "}
                                        {ov.sources === 1 ? "list" : "lists"}
                                    </span>
                                )}
                            </div>
                        </div>
                    </div>
                </div>
                <div className="flex items-center gap-1.5 shrink-0">
                    <a href="https://app.apollo.io/" target="_blank" rel="noopener noreferrer" className={secondaryBtn}>
                        <ExternalLinkIcon className="w-3 h-3" />
                        Open Apollo
                    </a>
                </div>
            </div>

            {needsKey && (
                <div className="mx-3 sm:mx-5 mb-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 flex flex-wrap items-center gap-2">
                    <AlertTriangleIcon className="w-3.5 h-3.5 text-amber-600 shrink-0" />
                    <span className="text-[12px] text-amber-800 flex-1 min-w-[200px]">
                        Apollo refused the saved API key{ov?.health_detail ? `: ${ov.health_detail}` : ""}. Imports and enrichment are
                        paused until you replace it.
                    </span>
                    {tab !== "key" && (
                        <button
                            type="button"
                            onClick={() => setTab("key")}
                            className="h-7 px-3 rounded-md bg-amber-500 hover:bg-amber-600 text-white text-[12px] font-medium transition-colors"
                        >
                            Replace key
                        </button>
                    )}
                </div>
            )}

            <div className="shrink-0 border-b border-slate-200 px-3">
                <ScrollStrip activeKey={tab} innerClassName="gap-1">
                    {TABS.map((t) => {
                        const active = tab === t.id;
                        return (
                            <button
                                key={t.id}
                                type="button"
                                data-active={active}
                                onClick={() => setTab(t.id)}
                                className={cn(
                                    "relative h-10 px-2.5 inline-flex items-center gap-1.5 text-[12.5px] outline-none whitespace-nowrap transition-colors shrink-0",
                                    active ? "text-slate-900 font-medium" : "text-slate-500 hover:text-slate-800",
                                )}
                            >
                                <t.icon className="w-3.5 h-3.5" />
                                {t.label}
                                {t.id === "imports" && importing && <Loader2Icon className="w-3 h-3 animate-spin text-sky-600" />}
                                {t.id === "enrich" && enriching && <Loader2Icon className="w-3 h-3 animate-spin text-sky-600" />}
                                {t.id === "key" && needsKey && <span className="size-1.5 rounded-full bg-amber-500" aria-label="Needs attention" />}
                                {active && (
                                    <motion.span
                                        layoutId="apollo-tab-underline"
                                        className="absolute left-1.5 right-1.5 -bottom-px h-0.5 rounded-full bg-sky-600"
                                        transition={{ type: "spring", duration: 0.3, bounce: 0.15 }}
                                    />
                                )}
                            </button>
                        );
                    })}
                </ScrollStrip>
            </div>

            <div className="flex-1 px-3 sm:px-5 py-5 pb-24">
                {tab === "imports" && <ImportTab connectionId={id} disabled={needsKey} />}
                {tab === "enrich" && <EnrichTab connectionId={id} />}
                {tab === "key" && <KeyTab connectionId={id} overview={ov} />}
            </div>
        </div>
    );
}

function BackLink() {
    return (
        <Link
            to="/app/integrations"
            className="inline-flex items-center gap-1 h-6 -ml-1.5 px-1.5 mb-1 rounded-md text-[11.5px] text-slate-500 hover:text-slate-900 hover:bg-slate-100 transition-colors"
        >
            <ArrowLeftIcon className="w-3 h-3" />
            Integrations
        </Link>
    );
}
