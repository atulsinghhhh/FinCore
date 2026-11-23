const formatters = new Map<string, Intl.NumberFormat>();

/** Formats minor units (paise) as currency, e.g. 100000 → ₹1,000.00. */
export function money(minor: number, currency = "INR"): string {
  let f = formatters.get(currency);
  if (!f) {
    f = new Intl.NumberFormat("en-IN", { style: "currency", currency });
    formatters.set(currency, f);
  }
  return f.format(minor / 100);
}

export function dateTime(iso: string): string {
  return new Intl.DateTimeFormat("en-IN", { dateStyle: "medium", timeStyle: "short", timeZone: "Asia/Kolkata" }).format(
    new Date(iso),
  );
}

export function shortId(id: string): string {
  return id.slice(0, 8);
}

export function count(n: number): string {
  return new Intl.NumberFormat("en-IN").format(n);
}

/** Compact Indian money for KPIs and axes: ₹11.9L, ₹1.2Cr, ₹950. */
export function moneyCompact(minor: number, currency = "INR"): string {
  return new Intl.NumberFormat("en-IN", { style: "currency", currency, notation: "compact", maximumFractionDigits: 1 }).format(minor / 100);
}

export function percent(r: number, digits = 1): string {
  return `${(r * 100).toFixed(digits)}%`;
}

/** Relative change between periods, or null when there is no baseline. */
export function delta(current: number, previous: number): number | null {
  if (!previous) return null;
  return (current - previous) / previous;
}
