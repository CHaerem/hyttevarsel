# hyttevarsel

Følger med på fullbookede DNT-hytter og sender e-post når en avbestilling
gjør plass. DNT lar folk bestille et år fram og avbestille gratis til fem
dager før (åtte på betjente hytter), så plassene dukker opp brått og
forsvinner fort. DNT har ikke noe varsel selv.

## Slik brukes den

Bruksanvisningen til Kristine: [docs/bruksanvisning.md](docs/bruksanvisning.md).
Oppsettet før første kjøring: [docs/oppsett.md](docs/oppsett.md).

**Legg til en hytte:** åpne hytta på hyttebestilling.dnt.no, velg datoer og
antall personer, trykk Del og send lenken på e-post til varseladressen.
Datoene ligger i lenken. Du får en bekreftelse som sier hvordan e-posten ble
tolket. Alternativt skrives datoen i emnet eller teksten:

| Skriv | Betyr |
|---|---|
| `12. juli`, `12.07`, `12.07.2027` | én natt, ankomst 12. juli |
| `12.–14. juli` | ankomst 12., avreise 14. |
| `2 netter`, `3 personer`, `vi er 3` | antall netter og personer |
| `en natt mellom 10. og 20. juli` | varsel for hver ankomstdag i perioden som blir ledig |
| `lørdager i august`, `helger i juni` | alle lørdager, eller alle fredager og lørdager |
| flere lenker i samme e-post | en rute: én natt på hver hytte, i rekkefølge; varsel når hele ruta er ledig |

**Varselet** har en stor «Bestill nå»-knapp med datoene og antall fylt inn,
og «Jeg har booket» som stopper overvåkingen (en ferdig utfylt e-post).

**Uten dato:** deler hun en lenke uten datoer, spør tjenesten «Hvilken dato?»
med hytta i emnet, og hun svarer bare med datoen.

**Auto-stopp:** et filter hos henne som videresender ordrebekreftelsene fra
DNT til varseladressen gjør at overvåkingen stopper av seg selv når hun har
bestilt. Bekreftelsen må komme fra `BOOKING_DOMAINS` (standard
`dnt.no,visbook.com`) med bekreftet avsender, eller være videresendt av
henne med DNTs avsenderlinje i teksten, og nevne hyttenavnet og
ankomstdatoen til en aktiv overvåking. Gjenkjenningen er laget uten en ekte
bekreftelse å se på (25. nov. 2026); juster `booking.go` når den finnes.
E-post fra DNT kan bare stoppe overvåkinger, aldri legge inn.

**Svar på en e-post fra tjenesten:** `stopp` stopper den hytta, `stopp alt`
stopper alle, `oversikt` gir lista. Søndag kveld kommer en ukesoppsummering.

Bare avsendere i `ALLOWED_SENDERS` (standard `MAIL_TO`) blir lyttet til, og
bare når Gmail har bekreftet avsenderen (DMARC, eller DKIM for avsenderens
domene). Ellers kunne hvem som helst skrive adressen hennes i From.

Nettsiden viser det samme og kan også legge inn hytter. Den har ingen
innlogging; tailnettet er porten, den skal aldri være på internett.

## Hvordan ledigheten hentes

Samme endepunkt som hyttebestilling.dnt.no bruker selv, uten innlogging:

```
GET https://hyttebestilling.dnt.no/api/booking/cabin-availability?cabinId=<id>&fromDate=YYYY-MM-DD&toDate=YYYY-MM-DD
```

`cabinId` er tallet i hytte-lenken (`/hytte/101075912`). `data.availability`
er ledige plasser per rom eller seng, minimum over hele oppholdet. Summen
sammenlignes med antall personer. Hytter uten nettbooking svarer med en
feil og avvises når de legges til. Navnet hentes fra hyttesidens `<title>`.

**Hvor ofte:** hvert opphold sjekkes hvert 2. minutt de siste ti dagene før
ankomst, der de fleste avbestillingene kommer, hvert 5. minutt inntil 30
dager før, og hvert 15. minutt lenger fram (`POLL_CADENCE`). Ett kall om
gangen med 3 sekunder mellom, og en egen User-Agent. Grenser per
overvåking: 6 hytter, 31 startdatoer, 40 kall per runde.

**Varsel:** når en startdato går fra fullt til ledig for alle hyttene i
den, høyst én gang i halvtimen per dato (en plass i noens handlekurv kan
forsvinne og komme tilbake).

Endepunktet er ikke et dokumentert API og kan endre seg. Har ingen kall
lyktes på en time, får Homelabs varselagent beskjed (høyst hver tolvte
time). `/healthz` sier bare om løkka går, for en omstart fikser ikke DNT.

## Drift

Én container, ingen database: tilstanden er `watches.json` i `DATA_DIR`.

| Variabel | Standard | |
|---|---|---|
| `LISTEN` | `:8080` | |
| `DATA_DIR` | `data` (`/data` i imaget) | må være et volum |
| `POLL_CADENCE` | `2m,5m,15m` | ≤10 dager, ≤30 dager, lenger fram |
| `SMTP_HOST`, `SMTP_PORT` | –, `587` | STARTTLS. Uten `SMTP_HOST` skrives e-post til loggen |
| `IMAP_HOST`, `IMAP_PORT` | –, `993` | innboksen. Uten `IMAP_HOST` leses den ikke |
| `SMTP_USER`, `SMTP_PASSWORD` | | Gmail-kontoen og et app-passord, for både SMTP og IMAP |
| `MAIL_FROM` | `SMTP_USER` | varseladressen |
| `MAIL_TO` | | kommaseparert; varsler og oppsummering |
| `ALLOWED_SENDERS` | `MAIL_TO` | hvem som kan legge inn og stoppe |
| `IMAP_AUTHSERV` | `mx.google.com` | hvem sin Authentication-Results som gjelder |
| `BOOKING_DOMAINS` | `dnt.no,visbook.com` | avsenderdomenene til ordrebekreftelsene |
| `BASE_URL` | | nettsiden, nederst i e-postene |
| `VARSEL_AGENT_DIR` | | mappe med `url` og `token` til varselagenten |

## Utvikling

```
nix develop -c go test ./...
nix develop -c go run .        # http://localhost:8080, e-post til loggen
```

Image bygges av CI på main og tagger (`ghcr.io/chaerem/hyttevarsel`,
amd64 og arm64). Manifestene ligger i Homelab-repoet under
`kubernetes/apps/hyttevarsel/`.
