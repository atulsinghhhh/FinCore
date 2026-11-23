"use client";

import { useEffect } from "react";

/**
 * Adds `.reached` to every `.will-animate` element the first time it scrolls
 * into view; the CSS in globals.css does the rest. Mounted once per page.
 */
export function Motion() {
  useEffect(() => {
    const els = document.querySelectorAll<HTMLElement>(".will-animate:not(.reached)");
    if (!("IntersectionObserver" in window)) {
      els.forEach((el) => el.classList.add("reached"));
      return;
    }
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            e.target.classList.add("reached");
            io.unobserve(e.target);
          }
        }
      },
      { rootMargin: "0px 0px -8% 0px", threshold: 0.15 },
    );
    els.forEach((el) => io.observe(el));
    return () => io.disconnect();
  }, []);
  return null;
}
