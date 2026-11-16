/** Said under a section once it has been saved. A live region, so a screen reader hears it. */
export function Saved({ text }: { text: string | undefined }) {
  return (
    <p className="small saved" role="status">
      {text}
    </p>
  );
}
