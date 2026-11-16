import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from "react";
import { errorMessage } from "../../api/client";
import { checkBranding, deleteLogo, putBranding, putLogo, type AdminSettings, type BrandingCheck } from "../../api/setup";
import { useSession } from "../../app/session";
import { InlineError } from "../../components/Errors";
import { Field } from "../../components/Field";
import { Icon, Logo } from "../../components/Icon";
import { useSubmit } from "../account/forms";
import { Preview } from "./Preview";
import { SetupFrame } from "./SetupFrame";

const HEX = /^#[0-9a-fA-F]{6}$/;
const FIELDS = ["name", "scheme", "accent"] as const;

/** One sentence for the failed check, with the measured ratio the server gave. */
function refusal(check: BrandingCheck, accent: string): string {
  const parts: string[] = [];
  if (check.failed.includes("on_accent")) {
    parts.push(`Button text on ${accent} has a contrast of ${check.on_accent_ratio.toFixed(1)} : 1`);
  }
  if (check.failed.includes("as_text")) {
    parts.push(`${accent} used as text on the page has a contrast of ${check.as_text_ratio.toFixed(1)} : 1`);
  }
  return `${parts.join(", and ")}. It needs at least 4.5 : 1.`;
}

export function BrandStep({
  settings,
  onSaved,
  onNext,
}: {
  settings: AdminSettings;
  onSaved: (s: AdminSettings) => void;
  onNext: () => void;
}) {
  const { reloadLibrary } = useSession();
  const [name, setName] = useState(settings.name);
  const [scheme, setScheme] = useState(settings.scheme);
  const [accent, setAccent] = useState(settings.accent);
  const [check, setCheck] = useState<BrandingCheck | null>(null);
  const [checkError, setCheckError] = useState<unknown>(null);
  const [logoError, setLogoError] = useState<unknown>(null);
  const [logoBusy, setLogoBusy] = useState(false);
  const file = useRef<HTMLInputElement>(null);
  const { busy, fieldErrors, formError, run } = useSubmit(FIELDS);

  const accentOk = accent === "" || HEX.test(accent);

  // The server judges the colours; this only asks, and keeps the newest answer.
  useEffect(() => {
    if (!accentOk) return;
    const ctl = new AbortController();
    checkBranding(scheme, accent, ctl.signal).then(
      (c) => (setCheck(c), setCheckError(null)),
      (err: unknown) => {
        if (!ctl.signal.aborted) setCheckError(err);
      },
    );
    return () => ctl.abort();
  }, [scheme, accent, accentOk]);

  const refused = accentOk && accent !== "" && check !== null && !check.ok;
  const chosen = settings.schemes.find((s) => s.id === scheme);
  const colours = check?.colours ?? chosen?.colours ?? {};

  async function upload(e: ChangeEvent<HTMLInputElement>) {
    const picked = e.target.files?.[0];
    e.target.value = "";
    if (!picked) return;
    setLogoBusy(true);
    setLogoError(null);
    try {
      onSaved(await putLogo(picked));
      await reloadLibrary();
    } catch (err) {
      setLogoError(err);
    } finally {
      setLogoBusy(false);
    }
  }

  async function removeLogo() {
    setLogoBusy(true);
    setLogoError(null);
    try {
      onSaved(await deleteLogo());
      await reloadLibrary();
    } catch (err) {
      setLogoError(err);
    } finally {
      setLogoBusy(false);
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!accentOk || refused) return;
    await run(async () => {
      onSaved(await putBranding({ name: name.trim(), scheme, accent: accent.toUpperCase() }));
      await reloadLibrary();
      onNext();
    });
  }

  const accentError = !accentOk ? "Enter a colour like #8A5A12, or leave it empty." : fieldErrors.accent;
  return (
    <SetupFrame step={2} title="Make it yours" intro="You can change all of this later in Settings.">
      <form onSubmit={submit} noValidate>
        <div className="layout">
          <section className="card setup-card">
            <InlineError error={formError} />
            <Field
              label="Library name"
              help="Shown on every screen and on library cards."
              value={name}
              onChange={setName}
              error={fieldErrors.name}
              maxLength={80}
            />
            <div className="divider" />
            <div className="field">
              <span className="lbl" id="logo-label">
                Logo
              </span>
              <div className="logo-drop" aria-labelledby="logo-label">
                {settings.logo_url ? <img className="logo" src={settings.logo_url} alt="" /> : <Logo />}
                <div className="logo-text">
                  <p className="small">
                    <b>{settings.logo_url ? "Your logo" : "Using the default logo"}</b>
                  </p>
                  <p className="help">JPEG, PNG or WebP, up to 2 MB. Square works best.</p>
                </div>
                <input ref={file} type="file" accept="image/jpeg,image/png,image/webp" className="sr" tabIndex={-1} aria-label="Logo file" onChange={upload} />
                <button type="button" className="btn btn-secondary" disabled={logoBusy} onClick={() => file.current?.click()}>
                  <Icon name="upload" />
                  {logoBusy ? "Uploading…" : "Upload"}
                </button>
                {settings.logo_url && (
                  <button type="button" className="btn btn-quiet" disabled={logoBusy} onClick={removeLogo}>
                    Remove
                  </button>
                )}
              </div>
              <InlineError error={logoError} />
            </div>
            <div className="divider" />
            <fieldset className="field scheme-set">
              <legend className="lbl">Colour scheme</legend>
              <div className="schemes" role="radiogroup" aria-label="Colour scheme">
                {settings.schemes.map((s) => (
                  <button
                    key={s.id}
                    type="button"
                    role="radio"
                    aria-checked={s.id === scheme}
                    className={s.id === scheme ? "scheme on" : "scheme"}
                    onClick={() => setScheme(s.id)}
                  >
                    <div className="sw">
                      {["--wood", "--primary", "--brass", "--paper"].map((t) => (
                        <i key={t} style={{ background: s.colours[t] }} />
                      ))}
                    </div>
                    <b>
                      {s.name}
                      {s.id === scheme && <Icon name="check" />}
                    </b>
                    <span>{s.description}</span>
                  </button>
                ))}
              </div>
              {fieldErrors.scheme && <p className="field-error">{fieldErrors.scheme}</p>}
            </fieldset>
            <div className="field accent-field">
              <label htmlFor="accent">
                Accent colour <span className="muted opt">(optional)</span>
              </label>
              <div className="accent">
                <span className="chip" style={{ background: accentOk && accent ? accent : colours["--primary"] }} />
                <input
                  id="accent"
                  className={accentError || refused ? "input input-error accent-input" : "input accent-input"}
                  value={accent}
                  onChange={(e) => setAccent(e.target.value.trim())}
                  placeholder="#8A5A12"
                  autoCapitalize="none"
                  spellCheck={false}
                  aria-invalid={accentError || refused ? true : undefined}
                  aria-describedby="accent-msg"
                />
                <button type="button" className="btn btn-quiet" onClick={() => setAccent("")} disabled={accent === ""}>
                  Reset
                </button>
              </div>
              <div id="accent-msg" aria-live="polite">
                {accentError && (
                  <span className="field-error">
                    <Icon name="alert" />
                    {accentError}
                  </span>
                )}
                {checkError !== null && (
                  <span className="field-error">
                    <Icon name="alert" />
                    {errorMessage(checkError)}
                  </span>
                )}
                {refused && check && (
                  <div className="alert" role="alert">
                    <Icon name="alert" />
                    <div>
                      <p className="t">Text would be hard to read on this colour</p>
                      <p className="small">
                        {refusal(check, accent.toUpperCase())} Try a darker shade, such as {check.suggestion}. Your scheme hasn't changed.
                      </p>
                      <button type="button" className="btn btn-secondary use-shade" onClick={() => setAccent(check.suggestion)}>
                        Use {check.suggestion}
                      </button>
                    </div>
                  </div>
                )}
              </div>
            </div>
          </section>
          <Preview name={name.trim() || settings.name} logoUrl={settings.logo_url} colours={colours} />
        </div>
        <div className="foot">
          <span />
          <div className="row">
            <button type="button" className="btn btn-quiet" onClick={onNext}>
              Skip
            </button>
            <button type="submit" className="btn btn-primary" disabled={busy || !accentOk || refused}>
              {busy ? "Saving…" : "Continue to borrowing rules"}
              <Icon name="right" />
            </button>
          </div>
        </div>
      </form>
    </SetupFrame>
  );
}

