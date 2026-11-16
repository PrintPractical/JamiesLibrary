import type { ReactNode } from "react";
import { useSession } from "../../app/session";
import { Logo } from "../../components/Icon";
import "./setup.css";

const STEPS = ["Your admin account", "Name & look", "Borrowing rules", "Invite people"];

/** The page every setup step sits in: the top bar, the progress, and the step's heading. */
export function SetupFrame({ step, title, intro, children }: { step: number; title: string; intro: string; children: ReactNode }) {
  const { library } = useSession();
  return (
    <>
      <header className="setup-top">
        {library.logo_url ? <img className="logo" src={library.logo_url} alt="" /> : <Logo />}
        <b>{library.name}</b>
        <span className="muted-on-wood">First-time setup</span>
      </header>
      <main className="setup-wrap">
        <ol className="progress" aria-label={`Setup progress, step ${step} of ${STEPS.length}`}>
          {STEPS.map((label, i) => (
            <li key={label} className={i + 1 < step ? "done" : i + 1 === step ? "cur" : ""} aria-current={i + 1 === step ? "step" : undefined}>
              <span className="lab">
                {i + 1} · {label}
              </span>
            </li>
          ))}
        </ol>
        <p className="eyebrow">
          Step {step} of {STEPS.length} · {STEPS[step - 1]}
        </p>
        <h1>{title}</h1>
        <p className="muted setup-intro">{intro}</p>
        {children}
      </main>
    </>
  );
}
