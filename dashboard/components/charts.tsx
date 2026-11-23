"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { money, moneyCompact } from "@/lib/format";

export type Unit = "money" | "count";
const axisFmt = (u: Unit) => (n: number) => (u === "money" ? moneyCompact(n) : new Intl.NumberFormat("en-IN", { notation: "compact" }).format(n));
const valueFmt = (u: Unit) => (n: number) => (u === "money" ? money(n) : new Intl.NumberFormat("en-IN").format(n));

// Hand-built SVG charts following DESIGN.md › Charts (from the dataviz method):
// thin marks, 2px lines, 10% area wash, hairline grid, ≤24px bars with 4px
// rounded data-ends, a hover layer on every chart, a data table fallback,
// and text that never wears the series color.

const INK = "var(--color-ink)";
const CONTEXT = "var(--color-chart-context)";
const PULSE = "var(--color-pulse)";

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [w, setW] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setW(Math.floor(e.contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, w] as const;
}

/** Rounds an axis maximum up to a clean step and returns 4 ticks. */
function ticks(max: number): number[] {
  if (max <= 0) return [0, 1, 2, 3, 4];
  const raw = max / 4;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw;
  return [0, 1, 2, 3, 4].map((i) => i * step);
}

function dayLabel(iso: string, long = false) {
  return new Intl.DateTimeFormat("en-IN", long ? { weekday: "short", day: "numeric", month: "short" } : { day: "numeric", month: "short" }).format(new Date(iso + "T00:00:00+05:30"));
}

function Tooltip({ x, width, children }: { x: number; width: number; children: ReactNode }) {
  const left = Math.min(Math.max(x - 80, 0), Math.max(width - 160, 0));
  return (
    <div className="pointer-events-none absolute top-0 z-10 w-40 rounded-lg border border-mist bg-white px-3 py-2 text-data shadow-sm" style={{ left }}>
      {children}
    </div>
  );
}

function DataTable({ head, rows }: { head: string[]; rows: (string | number)[][] }) {
  return (
    <details className="mt-3 text-data">
      <summary className="cursor-pointer text-caption uppercase text-warm-gray hover:text-ink">Data table</summary>
      <div className="mt-2 max-h-56 overflow-auto rounded-lg border border-mist">
        <table className="w-full text-left">
          <thead className="sticky top-0 bg-white text-caption uppercase text-warm-gray">
            <tr>{head.map((h) => <th key={h} className="px-3 py-2">{h}</th>)}</tr>
          </thead>
          <tbody className="divide-y divide-mist tabular-nums">
            {rows.map((r, i) => <tr key={i}>{r.map((c, j) => <td key={j} className="px-3 py-1.5">{c}</td>)}</tr>)}
          </tbody>
        </table>
      </div>
    </details>
  );
}

/** Single-series trend: line + 10% wash, crosshair tooltip, end value labelled. */
export function AreaTrend({ data, unit, height = 220, label }: { data: { day: string; value: number }[]; unit: Unit; height?: number; label: string }) {
  const format = axisFmt(unit);
  const full = valueFmt(unit);
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const pad = { l: 56, r: 16, t: 16, b: 28 };
  const max = Math.max(...data.map((d) => d.value), 0);
  const tk = ticks(max);
  const top = tk[tk.length - 1] || 1;
  const iw = Math.max(width - pad.l - pad.r, 1);
  const ih = height - pad.t - pad.b;
  const x = (i: number) => pad.l + (data.length <= 1 ? iw / 2 : (i / (data.length - 1)) * iw);
  const y = (v: number) => pad.t + ih - (v / top) * ih;
  const line = data.map((d, i) => `${i ? "L" : "M"}${x(i)},${y(d.value)}`).join("");
  const area = data.length ? `${line}L${x(data.length - 1)},${pad.t + ih}L${x(0)},${pad.t + ih}Z` : "";
  const every = Math.ceil(data.length / Math.max(2, Math.floor(iw / 72)));
  const last = data.length - 1;

  return (
    <div>
      <div ref={ref} className="relative" style={{ height }}>
        {width > 0 && (
          <svg width={width} height={height} role="img" aria-label={label}
            onPointerMove={(e) => {
              const bx = e.currentTarget.getBoundingClientRect().left;
              const i = Math.round(((e.clientX - bx - pad.l) / iw) * (data.length - 1));
              setHover(Math.min(Math.max(i, 0), last));
            }}
            onPointerLeave={() => setHover(null)}>
            {tk.map((t) => (
              <g key={t}>
                <line className="chart-grid" x1={pad.l} x2={width - pad.r} y1={y(t)} y2={y(t)} />
                <text className="chart-axis" x={pad.l - 8} y={y(t)} dy="0.32em" textAnchor="end">{format(t)}</text>
              </g>
            ))}
            {data.map((d, i) => ((i % every === 0 && last - i >= every / 2) || i === last) && (
              <text key={d.day} className="chart-axis" x={x(i)} y={height - 8} textAnchor={i === 0 ? "start" : i === last ? "end" : "middle"}>{dayLabel(d.day)}</text>
            ))}
            <path d={area} fill={INK} opacity={0.08} />
            <path d={line} fill="none" stroke={INK} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
            {hover !== null && <line x1={x(hover)} x2={x(hover)} y1={pad.t} y2={pad.t + ih} stroke={CONTEXT} strokeWidth={1} />}
            {[hover ?? last].filter((i) => i >= 0 && data[i]).map((i) => (
              <circle key={i} cx={x(i)} cy={y(data[i].value)} r={4.5} fill={i === last && hover === null ? PULSE : INK} stroke="var(--color-white)" strokeWidth={2} />
            ))}
          </svg>
        )}
        {hover !== null && data[hover] && (
          <Tooltip x={x(hover)} width={width}>
            <div className="text-caption uppercase text-warm-gray">{dayLabel(data[hover].day, true)}</div>
            <div className="mt-0.5 tabular-nums">{full(data[hover].value)}</div>
          </Tooltip>
        )}
      </div>
      <DataTable head={["Day", label]} rows={data.map((d) => [d.day, full(d.value)])} />
    </div>
  );
}

/** Two stacked series as emphasis: context in gray, the story in the accent. */
export function StackedColumns({ data, series, height = 220 }: { data: { day: string; a: number; b: number }[]; series: [string, string]; height?: number }) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const pad = { l: 44, r: 8, t: 12, b: 28 };
  const tk = ticks(Math.max(...data.map((d) => d.a + d.b), 0));
  const top = tk[tk.length - 1] || 1;
  const iw = Math.max(width - pad.l - pad.r, 1);
  const ih = height - pad.t - pad.b;
  const slot = iw / Math.max(data.length, 1);
  const bw = Math.min(24, Math.max(2, slot - 2));
  const h = (v: number) => (v / top) * ih;
  const every = Math.ceil(data.length / Math.max(2, Math.floor(iw / 72)));
  const fmt = (n: number) => new Intl.NumberFormat("en-IN").format(n);

  return (
    <div>
      <Legend items={[{ label: series[0], color: CONTEXT }, { label: series[1], color: PULSE }]} />
      <div ref={ref} className="relative mt-3" style={{ height }}>
        {width > 0 && (
          <svg width={width} height={height} role="img" aria-label={`${series[0]} and ${series[1]} per day`} onPointerLeave={() => setHover(null)}>
            {tk.map((t) => (
              <g key={t}>
                <line className="chart-grid" x1={pad.l} x2={width - pad.r} y1={pad.t + ih - h(t)} y2={pad.t + ih - h(t)} />
                <text className="chart-axis" x={pad.l - 8} y={pad.t + ih - h(t)} dy="0.32em" textAnchor="end">{fmt(t)}</text>
              </g>
            ))}
            {data.map((d, i) => {
              const cx = pad.l + slot * i + slot / 2;
              const ha = h(d.a);
              const hb = h(d.b);
              const base = pad.t + ih;
              const r = Math.min(4, bw / 2);
              const gap = d.a > 0 && d.b > 0 ? 2 : 0;
              return (
                <g key={d.day} opacity={hover === null || hover === i ? 1 : 0.45} onPointerEnter={() => setHover(i)}>
                  <rect x={cx - slot / 2} y={pad.t} width={slot} height={ih} fill="transparent" />
                  {/* bottom segment: square at the baseline, rounded only if it is the top */}
                  {ha > 0 && <path d={roundTop(cx - bw / 2, base - ha, bw, ha, hb > 0 ? 0 : r)} fill={CONTEXT} />}
                  {hb > 0 && <path d={roundTop(cx - bw / 2, base - ha - gap - hb, bw, hb, r)} fill={PULSE} />}
                  {((i % every === 0 && data.length - 1 - i >= every / 2) || i === data.length - 1) && (
                    <text className="chart-axis" x={cx} y={height - 8} textAnchor="middle">{dayLabel(d.day)}</text>
                  )}
                </g>
              );
            })}
          </svg>
        )}
        {hover !== null && data[hover] && (
          <Tooltip x={pad.l + slot * hover + slot / 2} width={width}>
            <div className="text-caption uppercase text-warm-gray">{dayLabel(data[hover].day, true)}</div>
            <Row color={CONTEXT} label={series[0]} value={fmt(data[hover].a)} />
            <Row color={PULSE} label={series[1]} value={fmt(data[hover].b)} />
          </Tooltip>
        )}
      </div>
      <DataTable head={["Day", series[0], series[1]]} rows={data.map((d) => [d.day, d.a, d.b])} />
    </div>
  );
}

/** Ranked horizontal bars in one hue, value at the tip. */
export function RankedBars({ items, unit }: { items: { label: string; value: number; href?: string; note?: string }[]; unit: Unit }) {
  const format = unit === "money" ? (n: number) => moneyCompact(n) : valueFmt(unit);
  const max = Math.max(...items.map((i) => i.value), 1);
  const [hover, setHover] = useState<number | null>(null);
  if (items.length === 0) return <p className="py-8 text-center text-data text-warm-gray">No data in this period.</p>;
  return (
    <ul className="space-y-3.5" onPointerLeave={() => setHover(null)}>
      {items.map((it, i) => {
        const label = it.href ? <a href={it.href} className="hover:underline hover:decoration-pulse">{it.label}</a> : it.label;
        return (
          <li key={it.label} onPointerEnter={() => setHover(i)} className={hover === null || hover === i ? "" : "opacity-50"}>
            <div className="mb-1.5 flex items-baseline justify-between gap-3 text-data">
              <span className="truncate">{label}</span>
              <span className="shrink-0 tabular-nums">{format(it.value)}{it.note && <span className="ml-2 text-warm-gray">{it.note}</span>}</span>
            </div>
            <div className="h-2.5 w-full">
              <div className="h-full rounded-r-[4px]" style={{ width: `${Math.max((it.value / max) * 100, it.value > 0 ? 1.5 : 0)}%`, background: i === 0 ? INK : CONTEXT }} />
            </div>
          </li>
        );
      })}
    </ul>
  );
}

/** 12–90 point trend in the de-emphasis hue, current value as an accent dot. */
export function Sparkline({ values, width = 112, height = 32 }: { values: number[]; width?: number; height?: number }) {
  const path = useMemo(() => {
    const max = Math.max(...values, 1);
    const min = Math.min(...values, 0);
    const span = max - min || 1;
    return values.map((v, i) => `${i ? "L" : "M"}${(i / Math.max(values.length - 1, 1)) * (width - 6) + 3},${height - 3 - ((v - min) / span) * (height - 6)}`).join("");
  }, [values, width, height]);
  if (values.length < 2) return null;
  const lastY = path.split("L").pop()!.split(",")[1];
  return (
    <svg width={width} height={height} aria-hidden className="overflow-visible">
      <path d={path} fill="none" stroke={CONTEXT} strokeWidth={1.5} strokeLinejoin="round" strokeLinecap="round" />
      <circle cx={width - 3} cy={Number(lastY)} r={3} fill={PULSE} stroke="var(--color-white)" strokeWidth={1.5} />
    </svg>
  );
}

function Legend({ items }: { items: { label: string; color: string }[] }) {
  return (
    <div className="flex flex-wrap gap-4 text-data text-warm-gray">
      {items.map((i) => (
        <span key={i.label} className="inline-flex items-center gap-2">
          <span className="h-2.5 w-2.5 rounded-sm" style={{ background: i.color }} aria-hidden />
          {i.label}
        </span>
      ))}
    </div>
  );
}

function Row({ color, label, value }: { color: string; label: string; value: string }) {
  return (
    <div className="mt-1 flex items-center justify-between gap-3">
      <span className="inline-flex items-center gap-1.5 text-warm-gray"><span className="h-2 w-2 rounded-sm" style={{ background: color }} />{label}</span>
      <span className="tabular-nums">{value}</span>
    </div>
  );
}

/** A rect path with only its top corners rounded (data-end), square at the base. */
function roundTop(x: number, y: number, w: number, h: number, r: number) {
  r = Math.min(r, h, w / 2);
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}
