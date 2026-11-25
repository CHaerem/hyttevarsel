# Oppsett

Engangsjobben før tjenesten kjører i klusteret. Manifestene ligger i
Homelab under `kubernetes/apps/hyttevarsel/`.

## 1. Gmail-kontoen for varslene

1. Lag en egen Gmail-konto, for eksempel `hyttevarsel.<noe>@gmail.com`. Den
   blir varseladressen Kristine sender til og får varsler fra.
2. Slå på totrinnsbekreftelse og lag et **app-passord**
   (myaccount.google.com → Sikkerhet → App-passord). Samme passord brukes
   for SMTP og IMAP.
3. IMAP er på som standard i nye kontoer. Sjekk under Innstillinger →
   Videresending og POP/IMAP.

Tjenesten merker e-post som lest når den er behandlet, også e-post den
ignorerer. Gmails bekreftelseskode for videresending (bruksanvisningen,
«Slutt å følge med av seg selv») ligger derfor som lest i innboksen.

## 2. Hemmelighetene (én kommando på mac-1)

```sh
curl -fsSL https://raw.githubusercontent.com/CHaerem/hyttevarsel/main/scripts/hemmeligheter.sh | bash
```

Skriptet spør etter varseladressen, app-passordet, Kristines adresse og
(valgfritt) tailnet-navnet for lenken til oversikten. Det prøver innloggingen mot Gmail, henter varselagentens
URL og token fra linux-4, krypterer begge hemmelighetene til eieren og
klusteret (`.sops.yaml` i Homelab) og åpner en PR i Homelab. Merge den, så
starter poden. Ingen verdi havner i klartekst på disk eller i argv.

## 3. Første test

1. Send en hyttelenke fra Kristines adresse til varseladressen. Innen et
   minutt skal bekreftelsen komme.
2. Følg med på en hytte som er ledig nå, så kommer et varsel med en gang.
   Svar «stopp» på det.
3. Loggen: `kubectl -n hyttevarsel logs deploy/hyttevarsel`.

## 4. Nettsiden (valgfri)

Oversikten er på `https://hyttevarsel.<tailnettet>.ts.net`. Kristine
trenger den ikke. Vil hun se den, deles maskinen «hyttevarsel» med henne i
Tailscale-adminen (Share), så får hun bare den.
