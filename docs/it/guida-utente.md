# Guida utente di Tolarix

Tolarix copia una wiki, il **Master**, in una o più altre wiki, i **Mirror**. Le
persone scrivono nel Master. Tolarix tiene allineati i Mirror: stesse pagine,
stessa gerarchia, link funzionanti, allegati e diagrammi, convertiti nel formato
di ciascuna wiki. La copia va in un solo verso: Tolarix non scrive mai nel
Master.

Questa guida descrive l'interfaccia web, che è la stessa in Docker e nell'app
desktop, e la riga di comando. Per installare un server consulta l'
[admin guide](../admin-guide.md) (in inglese). Per eseguire i flussi da cron o
dall'Utilità di pianificazione consulta [scheduling.md](../scheduling.md). La
versione inglese di questa guida è [user-guide.md](../user-guide.md).

- [Concetti](#concetti)
- [Collegare le wiki](#collegare-le-wiki)
- [Creare un flusso](#creare-un-flusso)
- [Pianificazioni](#pianificazioni)
- [Eseguire un flusso](#eseguire-un-flusso)
- [Leggere il resoconto di un'esecuzione](#leggere-il-resoconto-di-unesecuzione)
- [Limiti della conversione](#limiti-della-conversione)
- [La riga di comando](#la-riga-di-comando)
- [Domande frequenti](#domande-frequenti)

## Concetti

| Termine | Significato |
|---|---|
| **Connessione** | Come Tolarix raggiunge una wiki: indirizzo e credenziali. I segreti sono salvati cifrati e non vengono più mostrati. |
| **Flusso** | Copia un Master, o una sua parte, in uno o più Mirror, con opzioni e pianificazione. |
| **Master** | La wiki in cui si scrive. Tolarix la legge soltanto. |
| **Mirror** | Una wiki (o una cartella o pagina padre al suo interno) che riceve la copia. Le pagine create lì da Tolarix vengono sovrascritte a ogni esecuzione. |
| **Esecuzione** | Un'esecuzione di un flusso. Una **simulazione** calcola lo stesso piano e non scrive nulla. |

Tolarix ricorda quale pagina del Mirror corrisponde a quale pagina del Master.
Per questo scrive solo le pagine cambiate, trasforma le rinomine in spostamenti
e non tocca mai le pagine create da altri in un Mirror.

## Collegare le wiki

Apri **Connessioni → Aggiungi connessione**, scegli il tipo di wiki, compila i
dettagli e premi **Prova connessione** prima di salvare. Ogni tipo può fare da
Master o da Mirror.

L'utente o il token che dai a Tolarix ha bisogno della lettura sul Master e
della scrittura sul Mirror. Per limitare ciò che Tolarix può modificare, usa un
account dedicato con accesso solo agli spazi o alle wiki che sincronizza.

### Vault Obsidian

| Campo | Valore |
|---|---|
| Cartella del vault | La cartella che contiene il vault (quella con `.obsidian/`), per esempio `/srv/team-vault`. |

- La cartella deve trovarsi sulla macchina dove gira Tolarix. In Docker deve
  essere un volume montato, e si indica il percorso *dentro* il container (per
  esempio `/vaults/team`).
- Serve il permesso di lettura per un vault Master e di scrittura per un vault
  Mirror. L'immagine Docker gira con uid 65532.
- **Posizione nel Mirror:** *Cartella nel vault*. Vuota indica la radice del vault.

### Confluence Data Center / Server

| Campo | Valore |
|---|---|
| Indirizzo dello spazio o della pagina | Copialo dal browser. Uno spazio (`…/display/KEY`) sincronizza tutto lo spazio; una pagina (`…/display/KEY/Titolo`, `…/pages/viewpage.action?pageId=123`) sincronizza quella pagina e tutto ciò che sta sotto. |
| Personal access token o password | Un personal access token (avatar → **Profilo → Personal Access Tokens → Crea token**). |
| Nome utente | Solo per le versioni senza token. In quel caso il campo del token contiene la password dell'utente. |

- **Permessi:** *Visualizza* sullo spazio per un Master. Per un Mirror, *Aggiungi*
  pagine e allegati, più *Elimina* pagine e allegati se il flusso usa la regola
  di eliminazione. Le pagine archiviate vengono spostate sotto una pagina di
  archivio, non eliminate.
- **Posizione nel Mirror:** *Pagina padre*, come titolo o id numerico. Vuota
  indica la pagina della connessione, o la radice dello spazio.

### Confluence Cloud

| Campo | Valore |
|---|---|
| Indirizzo dello spazio o della pagina | Per esempio `https://tuo-sito.atlassian.net/wiki/spaces/KEY/overview`, o l'indirizzo di una pagina per sincronizzare lei e ciò che sta sotto. |
| Email | L'email dell'account Atlassian a cui appartiene il token. |
| API token | Un API token **classico**: [id.atlassian.com](https://id.atlassian.com) → **Sicurezza → Crea e gestisci token API → Crea token API**. I token con ambiti (scoped) non sono ancora supportati. |

- **Permessi:** come su Data Center: *Visualizza* per un Master; *Aggiungi* pagine
  e allegati per un Mirror, più *Elimina* per la regola di eliminazione.
- **Posizione nel Mirror:** *Pagina padre*. Vuota, con l'indirizzo di uno spazio,
  indica la homepage dello spazio, dove Cloud mette le nuove pagine.

### Azure DevOps — wiki di codice (Code wiki)

| Campo | Valore |
|---|---|
| Indirizzo della wiki o del repository | Apri la wiki e copia l'indirizzo (`https://dev.azure.com/org/progetto/_wiki/wikis/Nome`), oppure una cartella di un repository (`…/_git/repo?path=/docs&version=GBmain`). Funzionano anche gli indirizzi `org.visualstudio.com` e Azure DevOps Server (`https://host/tfs/Collection/…`). |
| Personal access token | **User settings → Personal access tokens → New token**, ambito **Code (Read & write)**. Per un solo Master basta **Code (Read)**. |

- Tolarix lavora tramite Git: ogni esecuzione che modifica qualcosa diventa **un
  solo commit** sul branch della wiki.
- Le **policy di branch** che richiedono pull request bloccano i push diretti.
  Per i Mirror usa un branch senza revisori obbligatori.
- **Posizione nel Mirror:** *Cartella nella wiki*, per esempio `Mirror/Team`.
  Vuota indica la radice della wiki.
- **Branch** (Mirror, facoltativo): invia le modifiche a un altro branch, per
  esempio `wiki/mirror`, invece che a quello della wiki. Se non esiste, la prima
  esecuzione che ha qualcosa da scrivere lo crea partendo dal branch della wiki
  (una simulazione non crea nulla). In quel caso il token deve avere anche il
  permesso *Create branch*. La wiki mostra quel branch solo se lo pubblichi come
  nuova versione della wiki (menu della wiki → **Publish new version**);
  altrimenti controlla le modifiche in **Repos** e uniscile con una pull request.
  È anche il modo per fare da Mirror verso un branch protetto da revisori
  obbligatori.
- Come Master, il branch viene dall'indirizzo: aggiungi `&version=GBbranch`
  all'indirizzo di un repository per leggere un altro branch.

### Azure DevOps — wiki di progetto (Project wiki)

| Campo | Valore |
|---|---|
| Indirizzo della wiki di progetto | `https://dev.azure.com/org/progetto/_wiki` (o `…/_wiki/wikis/progetto.wiki`). |
| Personal access token | Ambito **Code (Read & write)**, come per la wiki di codice. |

Il progetto deve avere già una wiki di progetto (**Overview → Wiki → Create
project wiki**). Tolarix scrive nel suo repository nascosto, sul branch
`wikiMaster`. Per il resto funziona come la wiki di codice.

### Export in una cartella locale

Tre tipi di connessione scrivono un Mirror in una cartella su disco invece che
in una wiki. Puoi poi fare commit della cartella in un repository, pubblicarla o
copiarla altrove. Possono essere solo Mirror.

| Tipo | Cosa ottieni |
|---|---|
| Local folder: Azure DevOps wiki | Gli stessi file che Tolarix scrive in una wiki di codice Azure DevOps (`Nome.md`, sottopagine in `Nome/`, `.order`, `.attachments/`). Fai push della cartella in un repository e pubblicala come wiki di codice. |
| Local folder: GitHub wiki | File per una wiki GitHub (clona `<repo>.wiki.git` nella cartella e fai push dopo l'esecuzione). |
| Local folder: Markdown | Markdown semplice con link relativi, per generatori di siti statici (MkDocs, Docusaurus…), GitLab o la consultazione in un repository. |

| Campo | Valore |
|---|---|
| Cartella di export | Una cartella esistente sulla macchina dove gira Tolarix, per esempio `/srv/export`. In Docker, un volume montato e il percorso dentro il container. |

- **Posizione nel Mirror:** *Cartella dentro la cartella di export*. Vuota
  significa la cartella di export stessa.
- La cartella deve già esistere ed essere scrivibile; **Prova connessione** lo
  verifica.
- Le modifiche vengono scritte tutte insieme alla fine dell'esecuzione. Una
  simulazione non scrive nulla.
- I file che Tolarix non gestisce (un `README.md`, una cartella `.git`…) non
  vengono toccati. Le modifiche fatte alle pagine esportate vengono
  sovrascritte all'esecuzione successiva, con un avviso.
- Le pagine eliminate nel Master vengono spostate in `Archived/`, come nelle
  wiki.

**Wiki GitHub.** GitHub mostra ogni pagina per nome, in qualunque cartella si
trovi, quindi i nomi delle pagine sono unici in tutto l'export: gli spazi
diventano `-`, i caratteri che GitHub non ammette vengono tolti e un duplicato
diventa `-(2)`. Le sottopagine vanno comunque in una cartella con il nome della
pagina padre. Tolarix scrive `_Sidebar.md` con l'albero delle pagine (nell'ordine
del Master); una sidebar scritta da te (senza il marcatore di Tolarix sulla
prima riga) non viene mai sostituita. Gli allegati vanno in `_attachments/`.

**Markdown.** Una pagina è `Nome.md`; una pagina con sottopagine diventa
`Nome/index.md` accanto a loro. Ogni pagina ha un `title` nel front matter, i
link sono relativi (`../Altro/Note.md#sezione`) e gli allegati vanno in
`assets/`. I file semplici non hanno un ordine delle pagine, quindi sono
ordinate per nome.

### Segreti

Il campo del token o della password è in sola scrittura. Quando modifichi una
connessione, il segreto resta com'è finché non scegli **Sostituisci segreto** o
**Rimuovi segreto**. Eliminare una connessione elimina il suo segreto; la wiki
non viene modificata. Una connessione usata da un flusso non si può eliminare.

## Creare un flusso

Apri **Flussi → Nuovo flusso**. La procedura guidata ha sei passaggi.

1. **Master.** Scegli la wiki in cui si scrive. Dopo il salvataggio il Master
   non si può cambiare, perché lo storico della sincronizzazione gli appartiene.
   Per replicare un'altra wiki crea un nuovo flusso.
2. **Cosa replicare.**
   - *Tutta la wiki*: tutte le pagine. Togli la spunta nell'albero per escludere
     delle pagine.
   - *Solo le pagine selezionate*: spunta pagine o cartelle; è incluso tutto ciò
     che sta sotto. Ogni pagina selezionata diventa una pagina di primo livello
     nei Mirror, senza i suoi antenati del Master. Le esclusioni vincono sempre
     sulle selezioni.
3. **Mirror.** Aggiungi una o più wiki e, per ciascuna, dove vanno le pagine
   (cartella o pagina padre). Un Mirror non può usare la connessione del Master,
   e la stessa wiki con la stessa posizione non può comparire due volte.
4. **Opzioni.**
   - *Quando una pagina viene rimossa dal Master*: **Archiviala nei Mirror**
     (predefinito: spostata in un'area `Archived`, così non si perde nulla),
     **Eliminala nei Mirror**, oppure **Lasciala nei Mirror** (resta lì e non
     viene più aggiornata).
   - *Copia gli allegati*: immagini e file usati dalle pagine (attivo per
     impostazione predefinita). Gli allegati vengono rimossi dai Mirror solo con
     la regola di eliminazione.
   - *Aggiungi un avviso alle pagine copiate*: una breve nota in cima a ogni
     pagina copiata invita i lettori a modificare il Master. Puoi scrivere un
     testo tuo; `{master}` diventa il nome del Master. Lascialo vuoto per il
     testo predefinito, che per ora è in inglese.
   - *Prendi in carico le pagine esistenti*: disattivato per impostazione
     predefinita. Da disattivato, una pagina creata da altri in un Mirror,
     proprio dove andrebbe una pagina copiata, blocca quella pagina con un
     conflitto. Da attivo, Tolarix la prende in carico e la sovrascrive.
5. **Pianificazione.** Vedi [Pianificazioni](#pianificazioni).
6. **Riepilogo.** Controlla il riepilogo e salva. Un flusso può essere **Attivo**
   o disattivato: un flusso disattivato non parte mai da solo, ma puoi sempre
   eseguirlo a mano.

Dopo il salvataggio, inizia con una **Simulazione**: mostra cosa verrebbe
creato, aggiornato, spostato, archiviato o eliminato, senza modificare nulla.

I flussi si possono anche importare da un file YAML con `tolarix flow import`.
Vedi [examples/flows.example.yaml](../examples/flows.example.yaml).

## Pianificazioni

La scheda **Semplice** copre i casi comuni:

| Scelta | Esempio |
|---|---|
| Solo quando lo avvio manualmente | il valore predefinito |
| Ogni N minuti o ore | ogni 5, 10, 15, 20 o 30 minuti; ogni 1, 2, 3, 4, 6, 8 o 12 ore (passi allineati all'orologio) |
| Ogni giorno alle… | ogni giorno alle 07:30 |
| In questi giorni della settimana alle… | dal lunedì al venerdì alle 07:30 |
| Ogni mese il giorno… alle… | il giorno 1 alle 06:00. I giorni 29–31 saltano i mesi più corti. |

La scheda **Avanzata (espressione cron)** accetta un'espressione cron a cinque
campi (`minuto ora giorno-del-mese mese giorno-della-settimana`, per esempio
`0 7 * * 1-5`) oppure `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`.
Secondi, `@every` e prefissi di fuso orario non sono accettati: usa il campo del
fuso orario.

- **Fuso orario**: tutti gli orari sono nel fuso del flusso (un nome IANA come
  `Europe/Rome`). Sotto il campo è indicato il fuso predefinito del server.
- **Anteprima**: mostra la pianificazione come frase, la sua forma cron e le
  prossime cinque esecuzioni, mentre scrivi.
- **Esegui con lo scheduler integrato**: attivo per impostazione predefinita.
  Disattivalo quando deve essere uno scheduler esterno (cron, systemd,
  Utilità di pianificazione) ad avviare il flusso con `tolarix run-due`. Gli
  orari restano gli stessi. Vedi [scheduling.md](../scheduling.md).

Gli orari persi (server spento) vengono recuperati con **una sola** esecuzione
quando il server torna attivo; quelli precedenti non vengono ripetuti. Se un
flusso è ancora in esecuzione quando arriva l'orario successivo, quell'orario
viene saltato.

Lo scheduler dell'app desktop funziona solo mentre l'app è aperta. Per flussi che
devono girare anche senza nessuno collegato, usa un server o `tolarix run-due`
da uno scheduler esterno.

## Eseguire un flusso

- **Esegui ora** e **Simulazione** si trovano nella panoramica e nella pagina del
  flusso.
- La pagina dell'esecuzione mostra il log in diretta. Puoi lasciare la pagina:
  l'esecuzione continua sul server. **Ferma esecuzione** annulla un'esecuzione
  avviata dall'interfaccia web; quelle avviate dalla riga di comando si fermano
  solo da lì.
- Un flusso non può girare due volte contemporaneamente, ovunque sia stato avviato:
  interfaccia web, scheduler integrato, `tolarix flow run` o `tolarix run-due`.
  Un secondo avvio mentre è in corso viene rifiutato, e la schermata o il codice
  di uscita (4) dicono che il flusso è già in esecuzione.
- Un server esegue al massimo due flussi pianificati alla volta. Le esecuzioni
  manuali non hanno limiti.

## Leggere il resoconto di un'esecuzione

Apri **Esecuzioni**, oppure un'esecuzione dalla panoramica o dalla pagina di un
flusso. La scheda **Resoconto** compare a esecuzione terminata; la scheda
**Log** mostra ogni riga.

**Stato**

| Stato | Significato | Codice di uscita CLI |
|---|---|---|
| Riuscita | tutto è stato scritto | 0 |
| Riuscita in parte | l'esecuzione è terminata, ma alcune pagine sono fallite (vedi i dettagli) | 3 |
| Fallita | l'esecuzione si è fermata: Master irraggiungibile, push rifiutato, … | 1 |
| Fermata | annullata da un utente | 1 |

Il resoconto di una simulazione ha la stessa forma e dice che non è stato
modificato nulla.

**Modifiche per Mirror**

| Modifica | Significato |
|---|---|
| Creata | nuova pagina nel Mirror |
| Aggiornata | la pagina del Master è cambiata, oppure qualcuno ha modificato la copia nel Mirror (vedi gli avvisi) |
| Spostata | la pagina è stata rinominata o spostata nel Master; anche quella del Mirror è stata spostata |
| Archiviata / Eliminata / Mantenuta | la pagina è stata rimossa dal Master; cosa succede dipende dalla regola scelta |
| Invariata | niente da fare (nascoste per impostazione predefinita: **Mostra le pagine invariate**) |
| Conflitto | la pagina non è stata scritta; i dettagli spiegano perché e come risolvere |

Gli **avvisi** elencano, pagina per pagina, tutto ciò che non si è convertito
perfettamente. I più comuni:

| Avviso | Significato |
|---|---|
| `mirror-edited` | qualcuno ha modificato la copia nel Mirror; Tolarix l'ha sovrascritta |
| `broken-link`, `link-out-of-scope`, `link-outside-master` | la destinazione del link manca, è fuori dall'ambito del flusso o fuori dal Master; il link è diventato testo semplice |
| `title-renamed` | in Confluence i titoli sono unici per spazio: la pagina ha ricevuto tra parentesi il titolo della pagina padre, poi un numero |
| `renamed` | il nome non è ammesso nel Mirror (caratteri non validi, nomi riservati, conflitti); è stato cambiato |
| `attachment-missing`, `attachment-not-mirrored` | un file citato non è stato trovato, oppure la copia degli allegati è disattivata |
| `render-failed`, `diagram-missing` | un diagramma draw.io non è stato convertito in PNG (vedi l'admin guide) |
| `confluence-macro`, `macro-unsupported` | una macro di Confluence non ha un equivalente nel Mirror |
| `scope-missing` | una pagina selezionata o esclusa non esiste più nel Master |

La riga degli allegati in fondo a ogni Mirror indica quanti file sono stati
caricati ed eliminati.

## Limiti della conversione

Ogni wiki ha funzioni che le altre non hanno. Quando una funzione non ha un
equivalente nel Mirror, Tolarix la trasforma in qualcosa di leggibile e segnala
un avviso. Non elimina mai contenuti in silenzio. Dettagli tecnici (in inglese):
[dev/connectors.md](../dev/connectors.md).

**Da qualsiasi Master a qualsiasi Mirror**

- I link a pagine fuori dall'ambito del flusso diventano testo semplice.
- Le modifiche fatte in un Mirror vengono sovrascritte dalla successiva esecuzione che scrive quella pagina.
- Se due pagine si scambiano di posto in un'unica modifica, entrambe segnalano un conflitto finché una delle due non viene spostata di nuovo (limite noto).
- Diagrammi draw.io: il file `.drawio` resta come allegato e viene mostrato un PNG, preso dall'anteprima accanto al diagramma, da un export server o da draw.io Desktop. Viene convertita solo la prima pagina del diagramma.

| Da → A | Cosa cambia |
|---|---|
| Obsidian → Confluence | I callout diventano pannelli info/tip/note/warning; quelli richiudibili diventano *expand*. Mermaid diventa un blocco `code` intitolato `mermaid` (Data Center non ha Mermaid integrato). Le note incorporate (`![[Nota]]`) diventano link. Il front matter non viene riportato (le etichette di Confluence non sono ancora supportate). L'ordine del Master si perde: Confluence ordina da sé le pagine figlie. |
| Obsidian → Azure DevOps | I callout diventano un'etichetta in grassetto in una citazione (`> **Tip:** …`), perché la wiki non ha callout. Mermaid diventa `::: mermaid`. Il front matter viene mantenuto. L'ordine delle pagine segue il Master (`.order`). |
| Confluence → Obsidian / Azure DevOps | `info`/`tip`/`note`/`warning`/`panel` diventano callout (in Azure DevOps, un'etichetta in grassetto). `expand` diventa un callout richiudibile. `toc` resta un indice. `children`, `pagetree` e `anchor` vengono omessi. `status` diventa il suo testo tra parentesi quadre (`[FATTO]`), le issue Jira diventano la loro chiave. Ogni altra macro diventa un blocco di codice `confluence-macro` con il suo XML, con un avviso. Lavagne (whiteboard), database ed embed di Confluence Cloud vengono saltati; le cartelle diventano pagine con solo il titolo. |
| Confluence → Confluence | Le macro, anche quelle sconosciute, vengono riscritte com'erano. I titoli vengono resi unici per spazio. |
| Azure DevOps → altre | `[[_TOC_]]` diventa l'indice del Mirror. `[[_TOSP_]]` (elenco delle sottopagine) viene omesso con un avviso. Le dimensioni delle immagini (`=300x200`) vengono mantenute dove il Mirror le supporta. |
| qualsiasi → Obsidian | I link vengono scritti come wikilink con il percorso completo nel vault. Gli allegati vanno nella cartella allegati del vault (o in `attachments/`); file con lo stesso nome ma contenuto diverso ricevono un breve hash nel nome. Obsidian non ha un ordine manuale: le pagine compaiono in ordine alfabetico. |
| qualsiasi → Azure DevOps | I caratteri speciali nei nomi delle pagine vengono codificati come fa la wiki (`Q&A: why?` → `Q&A%3A-why%3F`). Tutto ciò che un'esecuzione scrive finisce in un solo commit; se nel frattempo qualcuno ha fatto un push, l'esecuzione fallisce senza scrivere nulla e la successiva riparte da capo. |

## La riga di comando

Lo stesso programma `tolarix` fa da server e da riga di comando. I comandi
lavorano direttamente sulla cartella dei dati: non serve un server in
esecuzione. Aggiungi `--json` per un output leggibile dalle macchine e
`--data-dir` per usare un'altra cartella dei dati. I comandi e i loro messaggi
sono in inglese.

```sh
tolarix connection list                 # connessioni e loro tipo
tolarix connection test "Team wiki"     # verifica le impostazioni salvate
tolarix flow list                       # flussi, pianificazioni e ultime esecuzioni
tolarix flow run "Manuale" --dry-run    # cosa cambierebbe
tolarix flow run "Manuale"              # esegui ora e attendi
tolarix flow run --all                  # tutti i flussi attivi, uno dopo l'altro
tolarix run-due                         # i flussi che secondo la pianificazione sono da eseguire
tolarix runs list                       # esecuzioni recenti
tolarix runs show <id> --log            # resoconto e log di un'esecuzione
tolarix flow import flussi.yaml         # crea o aggiorna connessioni e flussi per nome
tolarix version
```

I codici di uscita sono stabili, quindi script e scheduler possono farci
affidamento:

| Codice | Significato |
|---|---|
| 0 | successo (per `run-due`, anche "niente da eseguire") |
| 1 | il comando o la sincronizzazione sono falliti |
| 2 | opzioni o argomenti non validi |
| 3 | la sincronizzazione è terminata, ma alcune pagine sono fallite |
| 4 | il flusso è già in esecuzione altrove |
| 5 | il flusso, la connessione o l'esecuzione non esistono |
| 6 | problema con la cartella dei dati, il database o la chiave segreta |

## Domande frequenti

**Posso modificare le pagine in un Mirror?** Puoi, ma la successiva esecuzione
che tocca la pagina sovrascrive la modifica e segnala `mirror-edited`. Modifica
il Master.

**Tolarix elimina pagine che non ha creato?** No. Modifica solo le pagine che ha
creato, più quelle prese in carico quando *Prendi in carico le pagine esistenti*
è attivo.

**Ho eliminato un flusso. Cosa succede ai Mirror?** Niente: le pagine copiate
restano dove sono. Un nuovo flusso non le riconosce come proprie, quindi attiva
*Prendi in carico le pagine esistenti* per la sua prima esecuzione, oppure
ripulisci prima il Mirror.

**Perché un flusso con un orario di prossima esecuzione non parte mai?**
Probabilmente lo scheduler integrato è disattivato ("Si esegue solo tramite
tolarix run-due") e nessuno scheduler esterno chiama `tolarix run-due`. Vedi
[scheduling.md](../scheduling.md).
