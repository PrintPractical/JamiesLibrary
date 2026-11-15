// A scheme is the colour for each token in styles/tokens.css. The defaults
// there are Hearth; ThemeProvider applies any other scheme the same way.

export const COLOUR_TOKENS = [
  "paper",
  "card",
  "card-2",
  "ink",
  "ink-2",
  "line",
  "line-2",
  "primary",
  "primary-hover",
  "primary-soft",
  "on-primary",
  "brass",
  "wood",
  "wood-2",
  "on-wood",
  "on-wood-2",
  "ok",
  "ok-soft",
  "warn",
  "warn-soft",
  "err",
  "err-soft",
] as const;

export type ColourToken = (typeof COLOUR_TOKENS)[number];
export type Scheme = Record<ColourToken, string>;

export const hearth: Scheme = {
  paper: "#f7f0e3",
  card: "#fffbf3",
  "card-2": "#fbf4e8",
  ink: "#2a1f17",
  "ink-2": "#5c4a3b",
  line: "#e4d5bf",
  "line-2": "#d3bfa2",
  primary: "#7a2e22",
  "primary-hover": "#63241a",
  "primary-soft": "#f4e2d7",
  "on-primary": "#fff8f0",
  brass: "#c08a2e",
  wood: "#3b2a1f",
  "wood-2": "#4a3527",
  "on-wood": "#f6ebdb",
  "on-wood-2": "#d9c6ac",
  ok: "#2e6243",
  "ok-soft": "#e2efe3",
  warn: "#7a4a00",
  "warn-soft": "#fbebc6",
  err: "#a1281b",
  "err-soft": "#fbe3dd",
};
