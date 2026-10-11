// Every background loop on the instance, grouped by the process that owns
// it. "Run now" only sets a flag the owning loop checks on its next poll,
// so the marker stays until that process clears it. Polls at 15s: the job
// table has no realtime event.

import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2, Play, Timer } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { EmptyState, Section, StatusBadge } from "@/components/ui/kit";
import { ErrorState } from "@/components/ErrorState";
import { DataTable, type Column } from "@/components/data/DataTable";
import { listJobs, runJob, type ScheduledJobRun } from "@/lib/api/client/admin/jobs";
import { TONE_TEXT, type Tone } from "@/lib/tones";
import { cn } from "@/lib/utils";
import { ExpandableText } from "@/app/dashboard/jobs/ExpandableText";
import { absolute, humanDuration, humanInterval, relative } from "@/app/dashboard/jobs/format";

const STATUS_TONE: Record<string, Tone> = {
    idle: "neutral",
    running: "info",
    ok: "success",
    error: "danger",
};

// backend and consumer first; anything new sorts after them by name.
const SERVICE_ORDER = ["backend", "consumer"];

function serviceRank(s: string): number {
    const i = SERVICE_ORDER.indexOf(s);
    return i === -1 ? SERVICE_ORDER.length : i;
}

export default function JobsPage() {
    const qc = useQueryClient();

    const { data, isLoading, error, refetch } = useQuery({
        queryKey: ["admin", "jobs"],
        queryFn: listJobs,
        refetchInterval: 15_000,
    });

    const run = useMutation({
        mutationFn: (name: string) => runJob(name),
        onSuccess: () => {
            toast.success("Requested; the loop picks it up within 15 seconds");
            qc.invalidateQueries({ queryKey: ["admin", "jobs"] });
        },
        onError: (err: Error) => toast.error(err.message || "Failed to request a run"),
    });

    const groups = useMemo(() => {
        const by = new Map<string, ScheduledJobRun[]>();
        for (const job of data?.data ?? []) {
            const list = by.get(job.service) ?? [];
            list.push(job);
            by.set(job.service, list);
        }
        return [...by.entries()]
            .sort(([a], [b]) => serviceRank(a) - serviceRank(b) || a.localeCompare(b))
            .map(([service, jobs]) => [service, jobs.sort((a, b) => a.name.localeCompare(b.name))] as const);
    }, [data]);

    const columns: Column<ScheduledJobRun>[] = [
        {
            id: "name",
            header: "Job",
            cell: (j) => <span className="font-mono text-xs font-medium text-foreground">{j.name}</span>,
            csv: (j) => j.name,
        },
        {
            id: "interval",
            header: "Interval",
            cell: (j) => <span className="whitespace-nowrap text-xs text-muted-foreground">{humanInterval(j.interval_seconds)}</span>,
            csv: (j) => j.interval_seconds,
        },
        {
            id: "last_run",
            header: "Last run",
            cell: (j) =>
                j.last_started_at ? (
                    <div className="whitespace-nowrap">
                        <div className="text-xs text-foreground tabular-nums" title={absolute(j.last_started_at)}>
                            {relative(j.last_started_at)}
                        </div>
                        <div className="text-[11.5px] text-muted-foreground tabular-nums">
                            {j.last_status === "running" ? (j.retired_at ? "recorded running" : "still running") : `took ${humanDuration(j.last_duration_ms)}`}
                        </div>
                    </div>
                ) : (
                    <span className="text-xs text-subtle-foreground">never</span>
                ),
            csv: (j) => j.last_started_at || "",
        },
        {
            id: "status",
            header: "Status",
            cell: (j) => (
                <div className="space-y-1">
                    <StatusBadge tone={j.retired_at ? "neutral" : STATUS_TONE[j.last_status] ?? "neutral"} dot={!j.retired_at && j.last_status !== "running"}>
                        {!j.retired_at && j.last_status === "running" && <Loader2 className="size-3 animate-spin" />}
                        {j.retired_at ? "retired" : j.last_status || "idle"}
                    </StatusBadge>
                    {j.retired_at && <div className="text-xs text-muted-foreground">Last recorded: {j.last_status || "idle"}</div>}
                </div>
            ),
            csv: (j) => j.retired_at ? `retired (last recorded: ${j.last_status})` : j.last_status,
        },
        {
            id: "next_run",
            header: "Next run",
            cell: (j) => j.retired_at ? (
                <span className="text-xs text-muted-foreground" title={`Retirement recorded ${absolute(j.retired_at)}; last schedule ${absolute(j.next_run_at)}`}>
                    Not scheduled
                </span>
            ) : (
                <div className="flex items-center gap-1.5 whitespace-nowrap">
                    <span className="text-xs text-muted-foreground tabular-nums" title={absolute(j.next_run_at)}>
                        {relative(j.next_run_at, "—")}
                    </span>
                    {j.run_requested_at && (
                        <StatusBadge
                            tone="accent"
                            title={`Run requested ${relative(j.run_requested_at)}; cleared when the ${j.service} picks it up`}
                        >
                            requested
                        </StatusBadge>
                    )}
                </div>
            ),
            csv: (j) => j.retired_at ? `retired; last schedule: ${j.next_run_at || "unknown"}` : j.next_run_at || "",
        },
        {
            id: "counts",
            header: "Runs / errors",
            align: "right",
            cell: (j) => (
                <span className="whitespace-nowrap text-xs tabular-nums text-foreground">
                    {j.run_count.toLocaleString()}
                    <span className="text-subtle-foreground"> / </span>
                    <span className={j.error_count > 0 ? cn("font-medium", TONE_TEXT.danger) : "text-muted-foreground"}>
                        {j.error_count.toLocaleString()}
                    </span>
                </span>
            ),
            csv: (j) => `${j.run_count}/${j.error_count}`,
        },
        {
            id: "last_error",
            header: "Last error",
            className: "max-w-md py-2",
            cell: (j) => <ExpandableText text={j.last_error} mono className="text-muted-foreground" />,
            csv: (j) => j.last_error,
        },
        {
            id: "actions",
            header: "",
            align: "right",
            cell: (j) => {
                const pending = run.isPending && run.variables === j.name;
                return (
                    <Button
                        size="xs"
                        variant="outline"
                        disabled={pending || !!j.run_requested_at || !!j.retired_at}
                        onClick={(e) => {
                            e.stopPropagation();
                            run.mutate(j.name);
                        }}
                        title={j.retired_at ? "Retired jobs do not accept run requests" : j.run_requested_at ? "A run is already requested" : `Ask the ${j.service} to run this loop now`}
                    >
                        <Play /> {pending ? "Requesting…" : "Run now"}
                    </Button>
                );
            },
        },
    ];

    const empty = !isLoading && !error && groups.length === 0;
    const total = data?.data?.length ?? 0;
    const failing = (data?.data ?? []).filter((j) => !j.retired_at && j.last_status === "error").length;

    return (
        <div>
            <PageHeader
                title="Jobs"
                meta={
                    !isLoading && !error && total > 0 ? (
                        <>
                            <span className="text-xs text-muted-foreground tabular-nums">{total}</span>
                            {failing > 0 && (
                                <StatusBadge tone="danger" dot>
                                    {failing} failing
                                </StatusBadge>
                            )}
                        </>
                    ) : undefined
                }
                description="Registered background loops and retained retired history. Active jobs run in their named process and pick up Run now at its next poll. Retired jobs retain their last recorded status but are not scheduled."
            />

            {error ? (
                <ErrorState error={error} title="Failed to load jobs" onRetry={() => refetch()} />
            ) : empty ? (
                <div className="surface-lit rounded-xl border border-border bg-card">
                    <EmptyState
                        icon={Timer}
                        title="No jobs have reported yet"
                        hint="Rows appear as soon as a service (the backend or the consumer) has booted on this build and registered its loops."
                    />
                </div>
            ) : isLoading ? (
                <DataTable columns={columns} rows={[]} getRowId={(j) => j.name} loading storageKey="admin.jobs" noun="jobs" />
            ) : (
                groups.map(([service, jobs]) => (
                    <Section
                        key={service}
                        className="first-of-type:mt-0"
                        title={
                            <span className="inline-flex items-baseline gap-2">
                                <span className="capitalize">{service}</span>
                                <span className="text-xs font-normal text-muted-foreground tabular-nums">
                                    {jobs.length} {jobs.length === 1 ? "loop" : "loops"}
                                </span>
                            </span>
                        }
                    >
                        <DataTable
                            columns={columns}
                            rows={jobs}
                            getRowId={(j) => j.name}
                            storageKey="admin.jobs"
                            csvName={`warmbly-jobs-${service}`}
                            noun="jobs"
                        />
                    </Section>
                ))
            )}
        </div>
    );
}
