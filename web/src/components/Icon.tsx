// The mockups' line icons (24px grid, 1.9 stroke), drawn inline so nothing is fetched.

const PATHS = {
  home: ["M3 10.5 12 3l9 7.5", "M5 9.5V20h5v-6h4v6h5V9.5"],
  search: ["M20 20l-3.5-3.5", "circle:11,11,7"],
  card: ["rect:2.5,5,19,14,2.5", "M6 9v6M8.5 9v6M11 9v6M13 9v6", "M16 10h3M16 13h3"],
  books: ["M4 4h4v16H4zM9.5 4h4v16h-4z", "m15.5 5.2 3.8-1 4 15.4-3.8 1z"],
  account: ["circle:12,8,4", "M4 21c1.5-4 4.5-6 8-6s6.5 2 8 6"],
  checkout: ["M14 4h4a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-4", "M3 12h11M10 8l4 4-4 4"],
  checkin: ["M10 4H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h4", "M21 12H10M14 8l-4 4 4 4"],
  catalog: ["M3 20h18M3 12h18M5 12V5h3v7M9 12V6h3v6M14.5 12l1.6-6.6 2.9.7L17.5 12M5 20v-6h3v6M10 20v-5h3v5"],
  patrons: ["circle:9,8,3.5", "M2.5 20c1-3.5 3.5-5.5 6.5-5.5s5.5 2 6.5 5.5", "M16 4.5a3.5 3.5 0 0 1 0 7M18 14.8c1.8.8 3 2.6 3.5 5.2"],
  overdue: ["circle:12,12,9", "M12 7v5l3 2"],
  settings: [
    "circle:12,12,3",
    "M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z",
  ],
  people: ["M3 8a2 2 0 0 0 2-2h14a2 2 0 0 0 2 2v2a2 2 0 0 0 0 4v2a2 2 0 0 0-2 2H5a2 2 0 0 0-2-2v-2a2 2 0 0 0 0-4z", "M14 6v12"],
  stats: ["M4 20V10M10 20V4M16 20v-7M21 20H3"],
  log: ["M6 3h12v18H6z", "M9 8h6M9 12h6M9 16h4"],
  switch: ["M7 4 3 8l4 4M3 8h14M17 12l4 4-4 4M21 16H7"],
  more: ["circle:5,12,1.2", "circle:12,12,1.2", "circle:19,12,1.2"],
  check: ["M5 12.5l4.5 4.5L19 7.5"],
  alert: ["circle:12,12,9", "M12 7.5v5.5M12 16.5h.01"],
  warning: ["M12 3.5 2.5 20h19z", "M12 10v4.5M12 17.5h.01"],
  info: ["circle:12,12,9", "M12 11v5.5M12 7.8h.01"],
  camera: ["M4 8h3l2-3h6l2 3h3v11H4z", "circle:12,13,3.5"],
  torch: ["M7 3h10v4l-3 4v10h-4V11L7 7z", "M12 14v2"],
  upload: ["M12 16V4M7 9l5-5 5 5", "M4 16v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3"],
  left: ["M15 6l-6 6 6 6"],
  right: ["m9 6 6 6-6 6"],
  scan: ["M3 8V5a2 2 0 0 1 2-2h3M16 3h3a2 2 0 0 1 2 2v3M21 16v3a2 2 0 0 1-2 2h-3M8 21H5a2 2 0 0 1-2-2v-3", "M7 8v8M10 8v8M13 8v8M16.5 8v8"],
  lock: ["rect:4,10,16,11,2", "M8 10V7a4 4 0 0 1 8 0v3"],
  clock: ["circle:12,12,9", "M12 7v5l3 2"],
  renew: ["M20 11a8 8 0 0 0-14.7-4.3M4 13a8 8 0 0 0 14.7 4.3", "M4 4v4h4M20 20v-4h-4"],
  pin: ["M12 21s7-6.2 7-12a7 7 0 0 0-14 0c0 5.8 7 12 7 12z", "circle:12,9,2.5"],
  close: ["M6 6l12 12M18 6 6 18"],
} as const;

export type IconName = keyof typeof PATHS;

function shape(spec: string, i: number) {
  if (spec.startsWith("circle:")) {
    const [cx, cy, r] = spec.slice(7).split(",");
    return <circle key={i} cx={cx} cy={cy} r={r} />;
  }
  if (spec.startsWith("rect:")) {
    const [x, y, width, height, rx] = spec.slice(5).split(",");
    return <rect key={i} x={x} y={y} width={width} height={height} rx={rx} />;
  }
  return <path key={i} d={spec} />;
}

export function Icon({ name }: { name: IconName }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {PATHS[name].map(shape)}
    </svg>
  );
}

/** The library's mark, as in the mockups. */
export function Logo() {
  return (
    <svg className="logo" viewBox="0 0 40 40" aria-hidden="true">
      <rect width="40" height="40" rx="10" fill="#7A2E22" />
      <path d="M8 13.5c4-1.6 8-1.3 12 1.2 4-2.5 8-2.8 12-1.2V29c-4-1.4-8-1.1-12 1.3C16 27.9 12 27.6 8 29z" fill="#FBEFD9" />
      <path d="M20 14.7v15.6" stroke="#C08A2E" strokeWidth="1.6" />
      <path
        d="M11 17.5c2.6-.6 4.8-.3 6.6.6M11 21c2.6-.6 4.8-.3 6.6.6M22.4 18.1c1.8-.9 4-1.2 6.6-.6M22.4 21.6c1.8-.9 4-1.2 6.6-.6"
        stroke="#C9AE86"
        strokeWidth="1.1"
        fill="none"
        strokeLinecap="round"
      />
      <path d="M26 9v8l-1.6-1.2L22.8 17V9z" fill="#C08A2E" />
    </svg>
  );
}
