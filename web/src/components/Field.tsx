import { useId, type InputHTMLAttributes } from "react";
import { Icon } from "./Icon";

export type FieldProps = Omit<InputHTMLAttributes<HTMLInputElement>, "id" | "onChange" | "value"> & {
  label: string;
  value: string;
  onChange: (value: string) => void;
  /** Said under the label, before anything is typed. */
  help?: string;
  /** What is wrong with this field, said under it. */
  error?: string;
};

/** A labelled text input with its help and error tied to it for screen readers (design system: inputs). */
export function Field({ label, value, onChange, help, error, ...input }: FieldProps) {
  const id = useId();
  const helpId = `${id}-help`;
  const errorId = `${id}-error`;
  const described = [help ? helpId : "", error ? errorId : ""].filter(Boolean).join(" ");
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {help && (
        <span className="help" id={helpId}>
          {help}
        </span>
      )}
      <input
        {...input}
        id={id}
        className={error ? "input input-error" : "input"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-invalid={error ? true : undefined}
        aria-describedby={described || undefined}
      />
      {error && (
        <span className="field-error" id={errorId}>
          <Icon name="alert" />
          {error}
        </span>
      )}
    </div>
  );
}
