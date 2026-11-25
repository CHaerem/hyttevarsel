# hyttevarsel

Følger med på fullbookede DNT-hytter og sender e-post når en avbestilling
gjør plass. DNT lar folk bestille et år fram og avbestille gratis til fem
dager før (åtte på betjente hytter), så plassene dukker opp brått og
forsvinner fort. DNT har ikke noe varsel selv.

## Slik brukes den

Oppskriften til Kristine: https://chaerem.github.io/hyttevarsel/
([docs/index.html](docs/index.html), GitHub Pages fra `main:/docs`; samme
innhold som tekst i [docs/bruksanvisning.md](docs/bruksanvisning.md)).
Oppsettet før første kjøring: [docs/oppsett.md](docs/oppsett.md).
UX-gjennomgangen som formet e-postene og siden: [docs/ux-review.md](docs/ux-review.md).

**Legg til en hytte:** velg datoer og antall personer på
hyttebestilling.dnt.no, trykk Del og send lenken på e-post til
varseladressen. Datoene og følget (voksne, ungdom og barn) leses fra
lenken. Datoen kan også skrives i emnet eller teksten: `12. juli`,
`12.–14. juli`, `en natt mellom 10. og 20. juli`, `lørdager i august`,
`helger i juni–august`, `helger i sommer`, `hele juli`, `2 netter`,
`3 personer`. Mangler datoen, spør tjenesten.

**Mange hytter:** flere lenker i samme e-post følges hver for seg, med de
samme datoene, og bekreftes i én e-post. Det hun allerede følger med på,
legges ikke inn en gang til. Skriver hun `rute`, blir lenkene i stedet en
tur fra hytte til hytte, med varsel når hele turen er ledig.

**Første svar:** én e-post. Er det fullt, en bekreftelse på hva det følges
med på. Er det ledig allerede, varselet direkte. Mange datoer (over seks
kall) bekreftes med en gang og sjekkes i bakgrunnen.

**Svar:** hver e-post om en hytte har svaradressen `<adresse>+h<id>@…`
(Gmail leverer pluss-adresser til samme innboks), så `stopp` i et svar
gjelder den hytta uten at et nummer står i emnet. `stopp` uten slik adresse
gjelder hytta hun nevner ved navn, eller den eneste hun har; ellers får hun
en liste med en Stopp-lenke per hytte. `stopp alt` stopper alle,
`oversikt` gir lista. Søndag kveld kommer oversikten av seg selv.

**Velkomst:** første gang tjenesten starter med e-post satt opp, sender
den «Hyttevarsel er klart» til `MAIL_TO` (slås av med `WELCOME=off`).
Lenken dit har varseladressen i ankeret (`#til=…`); siden selv er uten
adresse og ber søkemotorer la den være.

**Hvem blir hørt:** bare avsendere i `ALLOWED_SENDERS` (standard
`MAIL_TO`), og bare når Gmail har bekreftet avsenderen (DMARC, eller DKIM
for avsenderens domene). Et svar som siterer e-posten vår uten sitattegn
blir ikke tolket (`ownText` i parse.go).

**Ordrebekreftelser (eksperimentelt):** e-post fra `BOOKING_DOMAINS`, eller
videresendt med DNTs avsenderlinje i teksten, stopper overvåkingen når den
nevner hyttenavnet og ankomstdatoen. All annen e-post fra DNT ignoreres
uten svar. Gjenkjenningen er laget uten en ekte bekreftelse å se på, og
står derfor ikke i oppskriften til Kristine.

Nettsiden på tailnettet viser det samme og kan også legge inn hytter. Den
har ingen innlogging; tailnettet er porten.

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
overvåking: 6 hytter i en rute, 62 startdatoer, 62 opphold.

**Mange datoer for samme hytte:** fra fire opphold og opp hentes
kalenderen i stedet (`/api/booking/availability-calendar`, den DNT-siden
bruker i «Sjekk når hytta er ledig»): ett kall per hytte gir ledige
enheter per natt for minst et halvt år. Kalenderen teller rom eller senger
som enheter, ikke sengeplasser, så den brukes bare til å se hvor noe har
endret seg. Der noe er ledig og bildet er nytt, gjøres det nøyaktige
kallet. Ti hytter med alle helger i tre måneder blir da ti kall per runde
i stedet for 260.

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
| `MAIL_FROM` | `Hyttevarsel <SMTP_USER>` | avsender; adressen er varseladressen |
| `MAIL_TO` | | kommaseparert; varsler og oppsummering |
| `ALLOWED_SENDERS` | `MAIL_TO` | hvem som kan legge inn og stoppe |
| `IMAP_AUTHSERV` | `mx.google.com` | hvem sin Authentication-Results som gjelder |
| `BOOKING_DOMAINS` | `dnt.no,visbook.com` | avsenderdomenene til ordrebekreftelsene |
| `PAGE_URL` | GitHub Pages-siden | oppskriften det lenkes til i e-postene |
| `WELCOME` | | `off` slår av velkomst-e-posten |
| `VARSEL_AGENT_DIR` | | mappe med `url` og `token` til varselagenten |

## Utvikling

```
nix develop -c go test ./...
nix develop -c go run .        # http://localhost:8080, e-post til loggen
```

Image bygges av CI på main og tagger (`ghcr.io/chaerem/hyttevarsel`,
amd64 og arm64). Manifestene ligger i Homelab-repoet under
`kubernetes/apps/hyttevarsel/`.
