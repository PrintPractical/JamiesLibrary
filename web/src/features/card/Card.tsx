import { cardImage, getOwnCard } from "../../api/card";
import { useSession } from "../../app/session";
import { PageError } from "../../components/Errors";
import { Icon, Logo } from "../../components/Icon";
import { useLoad } from "../patrons/useLoad";
import "./card.css";

/** The patron's own library card: name, Code 128 barcode, QR code and number (CARD-1). */
export function Card() {
  const { library } = useSession();
  const { state, reload } = useLoad((signal) => getOwnCard(signal), "own");

  if (state.status === "error") return <PageError error={state.error} onRetry={reload} />;
  if (state.status === "loading") {
    return (
      <div className="stack">
        <h1>My library card</h1>
        <p className="muted">Loading…</p>
      </div>
    );
  }
  const { name, number } = state.data;
  return (
    <div className="stack">
      <h1>My library card</h1>
      <div className="card-wrap">
        <section className="lcard" aria-label={`Library card for ${name}`}>
          <div className="lcard-top">
            {library.logo_url ? <img className="logo" src={library.logo_url} alt="" /> : <Logo />}
            <div>
              <b>{library.name}</b>
              <span>Library card</span>
            </div>
          </div>
          <div className="lcard-body">
            <div className="lcard-name">{name}</div>
            <div className="lcard-bc">
              <img src={cardImage("barcode", number)} alt={`Barcode for card ${number}`} />
              <div className="lcard-num mono">{number}</div>
            </div>
            <div className="lcard-qr">
              <img src={cardImage("qr", number)} alt={`QR code for card ${number}`} />
            </div>
          </div>
        </section>
        <div className="card stack card-info">
          <div className="row">
            <Icon name="scan" />
            <p>Show this card at the desk. A librarian scans the barcode or the QR code, whichever their scanner reads.</p>
          </div>
          <div className="row">
            <Icon name="info" />
            <p className="small muted">Turn your screen brightness up if the scanner has trouble reading it.</p>
          </div>
          <div className="row">
            <Icon name="lock" />
            <p className="small muted">
              Lost your phone or think someone copied your card? Ask a librarian to issue a new one. The old code stops
              working straight away.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
