/** The plain-HTTP port the trust guide is served on unless the owner changed it (docs/install-lan.md). */
const GUIDE_PORT = 8080;

/**
 * The root certificate's fingerprint, to check a device's own display against
 * (security design T13). It says where to read it: this page is only trusted on a
 * device that already trusts the library, and the guide, which comes over plain
 * HTTP, is not a source to compare with.
 */
export function HttpsSection({ fingerprint }: { fingerprint: string | undefined }) {
  const url = new URL("/trust/", window.location.href);
  url.protocol = "http:";
  url.port = String(GUIDE_PORT);
  const guide = url.href;
  return (
    <section className="stack" aria-labelledby="set-https">
      <h2 id="set-https">Secure connection (HTTPS)</h2>
      <div className="card setup-card stack">
        {fingerprint ? (
          <>
            <p className="help">The library's own certificate authority has this SHA-256 fingerprint:</p>
            <p>
              <code className="fingerprint" data-testid="fingerprint">
                {fingerprint}
              </code>
            </p>
            <p className="small">
              To trust the library on a phone or computer, follow the <a href={guide}>trust guide</a> (it is served on port {GUIDE_PORT} unless you changed that). Then compare this fingerprint with the SHA-256 fingerprint
              that device shows for the certificate it installed. If they differ, remove the certificate.
            </p>
            <p className="small">
              Read this page only from a device that already trusts the library, never from the one you are setting up. That device can't tell a real page from a fake one yet. The
              server's console prints the same fingerprint.
            </p>
          </>
        ) : (
          <p className="help">
            This library has no certificate authority of its own: it is served with a certificate you provided, or behind a proxy that does the HTTPS. There is no
            fingerprint to compare here.
          </p>
        )}
      </div>
    </section>
  );
}
