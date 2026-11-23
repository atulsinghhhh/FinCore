"use client";

import { useRef, type ReactNode } from "react";

/** Horizontal, snap-scrolling card row with arrow controls. */
export function Slider({ children, label }: { children: ReactNode; label: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const move = (dir: 1 | -1) => ref.current?.scrollBy({ left: dir * Math.min(420, ref.current.clientWidth * 0.8), behavior: "smooth" });
  return (
    <div>
      <div ref={ref} className="no-scrollbar -mx-4 flex snap-x snap-mandatory gap-4 overflow-x-auto px-4 pb-2 md:-mx-6 md:px-6" role="region" aria-label={label} tabIndex={0}>
        {children}
      </div>
      <div className="mt-8 flex justify-end gap-3">
        {([-1, 1] as const).map((d) => (
          <button
            key={d}
            onClick={() => move(d)}
            aria-label={d < 0 ? "Previous" : "Next"}
            className="grid h-12 w-12 place-items-center rounded-full border border-white/30 text-white transition-colors hover:border-white hover:bg-white hover:text-ink"
          >
            {d < 0 ? "←" : "→"}
          </button>
        ))}
      </div>
    </div>
  );
}
