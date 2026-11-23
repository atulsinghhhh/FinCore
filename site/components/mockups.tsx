import type { ReactNode } from "react";

// Product mockups drawn in HTML/CSS: they stand in for photography and always
// show real FinCore behaviour.

function Shell({ label, children, tone = "light", className = "" }: { label: string; children: ReactNode; tone?: "light" | "dark" | "pulse"; className?: string }) {
  const tones = {
    light: "bg-white border border-mist text-ink",
    dark: "band",
    pulse: "band",
  };
  return (
    <div className={`relative flex h-[420px] w-[300px] shrink-0 flex-col overflow-hidden rounded-card p-6 text-left ${tones[tone]} ${className}`}>
      {tone === "pulse" && <div className="halo" style={{ top: "70%" }} />}
      <div className={`relative text-caption uppercase ${tone === "light" ? "text-warm-gray" : "text-fog"}`}>{label}</div>
      <div className="relative mt-auto">{children}</div>
    </div>
  );
}

export function CheckoutCard() {
  return (
    <Shell label="Checkout" tone="dark">
      <div className="mx-auto w-[190px] rounded-[28px] border-[6px] border-white/90 bg-white p-4 text-ink">
        <div className="mx-auto mb-4 h-1.5 w-12 rounded-full bg-mist" />
        <div className="text-caption uppercase text-warm-gray">Chai Point</div>
        <div className="mt-1 text-heading tracking-display">₹1,240</div>
        <div className="mt-4 space-y-2 text-[12px]">
          {["UPI", "Card", "Net banking"].map((m, i) => (
            <div key={m} className={`flex items-center justify-between rounded-lg border px-3 py-2 ${i === 0 ? "border-ink" : "border-mist text-warm-gray"}`}>
              {m}
              <span className={`h-3 w-3 rounded-full border ${i === 0 ? "border-ink bg-ink" : "border-fog"}`} />
            </div>
          ))}
        </div>
        <div className="mt-4 rounded-pill bg-ink py-2 text-center text-[12px] text-white">Pay securely</div>
      </div>
    </Shell>
  );
}

export function LedgerCard() {
  const rows = [
    ["psp_clearing", "Dr", "1,000.00"],
    ["merchant:payable", "Cr", "980.00"],
    ["fee_revenue", "Cr", "20.00"],
  ];
  return (
    <Shell label="Ledger entry">
      <div className="text-heading-sm">Capture pay_8c14</div>
      <table className="mt-5 w-full text-[13px]">
        <tbody className="divide-y divide-mist border-y border-ink">
          {rows.map(([a, s, v]) => (
            <tr key={a}>
              <td className="py-2.5 font-mono text-[12px]">{a}</td>
              <td className="py-2.5 text-warm-gray">{s}</td>
              <td className="py-2.5 text-right tabular-nums">₹{v}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="mt-4 flex items-center gap-2 text-caption uppercase">
        <span className="h-1.5 w-1.5 rounded-full bg-pulse" /> Balanced
      </div>
    </Shell>
  );
}

export function ReplayCard() {
  return (
    <Shell label="Idempotency" tone="dark">
      <pre className="whitespace-pre-wrap font-mono text-[12px] leading-relaxed text-fog">
        <span className="text-white">POST</span> /v1/payments{"\n"}Idempotency-Key: order-42{"\n\n"}
        <span className="text-white">201</span> Created{"\n"}
        <span className="text-pulse">Idempotent-Replayed: true</span>
      </pre>
      <p className="mt-6 text-heading-sm">Retried 3×.<br />Charged once.</p>
    </Shell>
  );
}

export function RefundCard() {
  return (
    <Shell label="Refund">
      <div className="border-b border-dashed border-fog pb-4">
        <div className="text-caption uppercase text-warm-gray">Refund to customer</div>
        <div className="mt-1 text-display tracking-display">₹200</div>
      </div>
      <dl className="mt-4 space-y-2 text-[13px]">
        <div className="flex justify-between"><dt className="text-warm-gray">Merchant pays</dt><dd>₹196.00</dd></div>
        <div className="flex justify-between"><dt className="text-warm-gray">Fee returned</dt><dd>₹4.00</dd></div>
        <div className="flex justify-between"><dt className="text-warm-gray">Status</dt><dd className="rounded-tag bg-ink px-2 text-[11px] uppercase text-white">succeeded</dd></div>
      </dl>
    </Shell>
  );
}

export function PayoutCard() {
  return (
    <Shell label="Payout" tone="pulse">
      <div className="text-display tracking-display">₹784</div>
      <p className="mt-2 text-label text-fog">to HDFC •••• 4821</p>
      <ol className="mt-6 space-y-3 text-[13px]">
        {["Held from available balance", "In transit to bank", "Confirmed by bank"].map((s, i) => (
          <li key={s} className="flex items-center gap-3">
            <span className={`h-2.5 w-2.5 rounded-full ${i === 2 ? "bg-pulse" : "bg-white"}`} />
            {s}
          </li>
        ))}
      </ol>
    </Shell>
  );
}

export function ReconCard() {
  return (
    <Shell label="Reconciliation">
      <div className="font-mono text-[12px] text-warm-gray">setl_2026-11-24.csv</div>
      <div className="mt-3 text-display tracking-display">2/2</div>
      <div className="text-label text-warm-gray">lines matched</div>
      <div className="mt-6 space-y-2 text-[13px]">
        <div className="flex justify-between border-t border-mist pt-2"><span>Gross settled</span><span>₹3,749.00</span></div>
        <div className="flex justify-between"><span className="text-warm-gray">Processor fees</span><span>−₹88.48</span></div>
        <div className="flex justify-between border-t border-ink pt-2"><span>Net to bank</span><span>₹3,660.52</span></div>
      </div>
    </Shell>
  );
}

export function WebhookCard() {
  return (
    <Shell label="Webhooks" tone="dark">
      <pre className="whitespace-pre-wrap font-mono text-[12px] leading-relaxed text-fog">
        {`{
  "id": "evt_1042",
  "type": `}
        <span className="text-white">&quot;payment.captured&quot;</span>
        {`,
  "data": { "amount": 100000 }
}`}
      </pre>
      <p className="mt-5 break-all font-mono text-[11px] text-fog">FinCore-Signature: t=1795…,v1=9f2c…</p>
      <p className="mt-4 text-heading-sm">Signed. Retried. Never lost.</p>
    </Shell>
  );
}

export function ZeroCard() {
  return (
    <Shell label="Under load">
      <div className="text-[180px] leading-[0.8] tracking-display">0</div>
      <p className="mt-4 text-heading-sm">duplicate charges</p>
      <p className="mt-2 text-label text-warm-gray">across 14,825 concurrent requests</p>
    </Shell>
  );
}

export const heroCards = [CheckoutCard, LedgerCard, ReplayCard, RefundCard, PayoutCard, ReconCard, WebhookCard, ZeroCard];
