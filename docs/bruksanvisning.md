# Hyttevarsel: slik bruker du det

Hyttevarselet følger med på DNT-hytter som er fulle, og sender deg en
e-post med en «Bestill nå»-knapp i det noen avbestiller. Du styrer alt med
e-post til varseladressen (Christopher gir deg den). Du trenger ingen app
og ingen innlogging.

## Første gang

1. **Lagre varseladressen som kontakt**, for eksempel «Hyttevarsel», så den
   er lett å velge.
2. **Slå på varsler for den adressen** i e-postappen (på iPhone: legg den til
   som VIP i Mail), så varselet merkes med en gang. De fleste plassene blir
   tatt fort.
3. **Lag «Følg med»-knappen** i delingsmenyen, så holder det med ett trykk:

   **iPhone (Snarveier-appen):**
   1. Åpne Snarveier og trykk **+** for en ny snarvei. Kall den «Følg med på
      hytta».
   2. Trykk **ⓘ** og slå på **Vis i delingsark**. Velg at den tar imot
      **URL-er** og **Safari-nettsider**.
   3. Legg til handlingen **Send e-post**. Mottaker: varseladressen. Emne:
      «Følg med». Innhold: **Snarveisinndata**.
   4. Trykk pilen på handlingen og slå **av** «Vis skjema for e-post», så
      sendes den i bakgrunnen.
   5. Valgfritt: legg til **Vis varsel** med teksten «Sendt! Bekreftelsen
      kommer om et minutt.»

   Handlingen sender fra Mail-appen, så Mail må ha e-postkontoen din lagt
   inn, også om du vanligvis bruker Gmail-appen.

   **Android:** Del lenken med Gmail til varseladressen én gang. Etterpå
   dukker «Hyttevarsel» opp øverst i delingsmenyen som en snarvei, og da
   blir det Del → Hyttevarsel → Send.

## Når en hytte er full

1. Åpne hytta på [hyttebestilling.dnt.no](https://hyttebestilling.dnt.no)
   og velg datoer og antall personer, akkurat som når du bestiller.
2. Trykk **Del** og velg **Følg med på hytta** (eller send lenken på
   e-post til varseladressen). Du trenger ikke skrive noe, for datoene
   ligger i lenken.
3. Etter omtrent et minutt får du en bekreftelse, for eksempel «Følger med
   på Skåla: lørdag 12. juli 2027, 1 natt, 2 personer». Står det noe feil,
   svarer du «stopp» og sender lenken på nytt.

Delte du lenken uten å velge datoer, spør tjenesten «Hvilken dato?». Da
svarer du bare med datoen, for eksempel «12. juli».

Er det ledig allerede, får du varselet med en gang.

## Når noen avbestiller

1. Du får en e-post med emnet «Ledig: Skåla lør 12. juli» og en stor rød
   **Bestill nå**-knapp. Den åpner DNT-siden med datoene og antall
   personer fylt inn. Bestill som vanlig, og helst med en gang.
2. Har du satt opp filteret under «Slutt å følge med av seg selv», stopper
   tjenesten selv når ordrebekreftelsen fra DNT kommer. Ellers trykker du
   **Jeg har booket** i varselet og sender e-posten som dukker opp.

Blir plassen tatt av noen andre før du rekker det, fortsetter tjenesten å
følge med og sier fra neste gang.

## Skrive datoene selv

Du kan skrive i emnet eller teksten når du sender en lenke:

| Du skriver | Tjenesten følger med på |
| --- | --- |
| `12. juli` eller `12.07` | én natt med ankomst 12. juli |
| `12.–14. juli` | ankomst 12., avreise 14. juli (2 netter) |
| `2 netter`, `3 personer`, `vi er 3` | antall netter og personer |
| `en natt mellom 10. og 20. juli` | hver ankomstdag i perioden; du får varsel for hver dato som blir ledig |
| `lørdager i august` | alle lørdager i august |
| `helger i juni` | alle fredager og lørdager i juni |
| flere hyttelenker i samme e-post | en rute med én natt på hver hytte, i rekkefølgen du limte dem inn. Varselet kommer når hele ruta er ledig, med én bestillingsknapp per hytte |

Uten årstall gjelder neste gang datoen kommer. DNT åpner for bestilling
omtrent et år fram.

## Svar du kan sende

| Svar | Hva skjer |
| --- | --- |
| `stopp` (svar på et varsel eller en bekreftelse) | tjenesten slutter å følge med på den hytta |
| `stopp alt` | alle overvåkingene stoppes |
| `oversikt` | du får lista over alt du følger med på |

Hver søndag kveld kommer en kort oversikt over det du følger med på. Gamle
datoer rydder seg selv.

## Slutt å følge med av seg selv

Sett opp ett filter som videresender ordrebekreftelsene fra DNT til
varseladressen. Da stopper tjenesten selv når du har bestilt, og du
slipper å trykke «Jeg har booket».

**Gmail** (på nett, ikke i appen):
1. Innstillinger → **Se alle innstillinger** → **Videresending og POP/IMAP**
   → **Legg til en videresendingsadresse**: varseladressen. Gmail sender en
   kode dit; Christopher finner den i innboksen til varseladressen.
2. Søk etter `from:dnt.no`, trykk **Vis søkealternativer** → **Opprett
   filter** → **Videresend til** varseladressen.

**iCloud Mail** (på icloud.com/mail): ⚙ → **Regler** → **Legg til regel**:
hvis avsender inneholder `dnt.no`, videresend til varseladressen.

Vi har ikke sett en ekte ordrebekreftelse fra DNT ennå. Første gang du
bestiller, ser vi om den blir gjenkjent. Kommer det ingen «Booket! God
tur»-e-post, trykker du «Jeg har booket» som før, og Christopher får
justert gjenkjenningen.

## Hvem ser hva

Tjenesten hører bare på e-post fra din adresse, og bare når e-posttjenesten
din bekrefter at det virkelig er deg som har sendt den. Den kjører hjemme
hos Christopher og sjekker DNT med samme forespørsel som nettsiden gjør
når du ser på en hytte.
