# hyttevarsel

Følger med på fullbookede DNT-hytter og sender e-post når en avbestilling
gjør plass. DNT lar folk bestille et år fram og avbestille gratis til fem
dager før (åtte på betjente hytter), så plassene dukker opp brått og
forsvinner fort. DNT har ikke noe varsel selv.

## Slik brukes den

Åpne nettsiden, lim inn lenken til hytta fra hyttebestilling.dnt.no, velg
ankomstdato, antall netter og personer, og trykk «Følg med». Når det blir
plass til hele følget gjennom hele oppholdet, kommer en e-post med lenke
rett til bestillingen, med datoene fylt inn. Et nytt varsel for samme
overvåking kommer først når plassen har vært borte og kommer tilbake, og
aldri oftere enn hver halvtime. Overvåkinger der ankomsten har vært,
stoppes og fjernes etter to uker.

## Hvordan ledigheten hentes

Samme endepunkt som hyttebestilling.dnt.no bruker selv, uten innlogging:

```
GET https://hyttebestilling.dnt.no/api/booking/cabin-availability?cabinId=<id>&fromDate=YYYY-MM-DD&toDate=YYYY-MM-DD
```

`cabinId` er tallet i hytte-lenken (`/hytte/101075912`). `data.availability`
er ledige plasser per rom eller seng, minimum over hele oppholdet. Summen
sammenlignes med antall personer. Hytter uten nettbooking svarer med en
feil og avvises når de legges til. Navnet på hytta hentes fra hyttesidens
`<title>`, én gang.

Skånsomhet: ett kall per overvåking per runde, ett om gangen med 3 sekunder
mellom, runder omtrent hvert 5. minutt (±20 %). En egen User-Agent sier hva
klienten er. Endepunktet er ikke et dokumentert API og kan endre seg; da
viser siden «Feil ved sjekk» og `/healthz` holder seg grønn, så sjekk loggen.

## Drift

Én container, ingen database: tilstanden er `watches.json` i `DATA_DIR`.

| Variabel | Standard | |
|---|---|---|
| `LISTEN` | `:8080` | |
| `DATA_DIR` | `data` (`/data` i imaget) | må være et volum |
| `POLL_INTERVAL` | `5m` | minst `1m` |
| `SMTP_HOST`, `SMTP_PORT` | –, `587` | STARTTLS. Uten `SMTP_HOST` skrives varslene bare til loggen |
| `SMTP_USER`, `SMTP_PASSWORD` | | for Gmail: et app-passord |
| `MAIL_FROM` | `SMTP_USER` | |
| `MAIL_TO` | | kommaseparert |
| `BASE_URL` | | lenke til nettsiden nederst i e-posten |

`GET /healthz` svarer 503 hvis ingen runde er fullført på over 3 × intervallet
pluss 10 minutter.

Nettsiden har ingen innlogging. Tailnettet er porten: den skal bare være
nåbar der, aldri på internett.

## Utvikling

```
nix develop -c go test ./...
nix develop -c go run .        # http://localhost:8080, varsler til loggen
```

Image bygges av CI på main og tagger (`ghcr.io/chaerem/hyttevarsel`,
amd64 og arm64). Manifestene ligger i Homelab-repoet under
`kubernetes/apps/hyttevarsel/`.
