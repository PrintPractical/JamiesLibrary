import { useState } from "react";
import { ApiError, errorMessage } from "../../api/client";

/**
 * Runs a form's submit and sorts a failure: an API error that names one of the
 * form's fields belongs under that field, anything else is the form's own error.
 */
export function useSubmit(fields: readonly string[]) {
  const [busy, setBusy] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<unknown>(null);

  async function run(action: () => Promise<void>): Promise<boolean> {
    setBusy(true);
    setFieldErrors({});
    setFormError(null);
    try {
      await action();
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.field && fields.includes(err.field)) {
        setFieldErrors({ [err.field]: errorMessage(err) });
      } else {
        setFormError(err);
      }
      return false;
    } finally {
      setBusy(false);
    }
  }

  return { busy, fieldErrors, formError, run };
}
