import type { ReactNode } from "react";
import { useSession } from "../../app/session";
import { Logo } from "../../components/Icon";

/** The frame of the screens a visitor sees: sign in, register, forced password change. */
export function AuthPage({ title, children }: { title: string; children: ReactNode }) {
  const { library } = useSession();
  return (
    <main className="content auth">
      <div className="auth-brand">
        {library.logo_url ? <img className="logo" src={library.logo_url} alt="" /> : <Logo />}
        <span className="name">{library.name}</span>
      </div>
      <section className="card auth-card">
        <h1>{title}</h1>
        {children}
      </section>
    </main>
  );
}
