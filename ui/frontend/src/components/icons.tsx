// Stroke icons, drawn at 24×24 and inheriting the text colour.
import type { ReactNode } from "react";

const paths = {
  database: (
    <>
      <ellipse cx="12" cy="6" rx="7" ry="3" />
      <path d="M5 6v12c0 1.66 3.13 3 7 3s7-1.34 7-3V6M5 12c0 1.66 3.13 3 7 3s7-1.34 7-3" />
    </>
  ),
  code: <path d="M8 9l-3 3 3 3M16 9l3 3-3 3M13 7l-2 10" />,
  history: (
    <>
      <path d="M3 12a9 9 0 1 0 3-6.7L3 8" />
      <path d="M3 3v5h5M12 7v5l3 2" />
    </>
  ),
  plug: <path d="M9 3v5M15 3v5M7 8h10v4a5 5 0 01-10 0V8zM12 17v4" />,
  audit: <path d="M9 4h6v3H9zM9 5.5H6v15h12v-15h-3M9 12h6M9 16h4" />,
  plus: <path d="M12 5v14M5 12h14" />,
  play: <path d="M7 5l12 7-12 7V5z" />,
  stop: <rect x="6" y="6" width="12" height="12" rx="1.5" />,
  lock: (
    <>
      <rect x="5" y="11" width="14" height="10" rx="2" />
      <path d="M8 11V7a4 4 0 0 1 8 0v4" />
    </>
  ),
  check: <path d="M5 12l5 5 9-10" />,
  alert: <path d="M12 4l9 16H3l9-16zM12 10v4M12 17.5v.01" />,
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof paths;

export function Icon({ name, size = 16, strokeWidth = 1.75 }: { name: IconName; size?: number; strokeWidth?: number }) {
  return (
    <svg
      aria-hidden="true"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {paths[name]}
    </svg>
  );
}
