# FileSwiper

Trier un dossier du PC depuis l'iPhone, façon Tinder : **droite = garder**, **gauche = supprimer**.

- `server/` — serveur Go, un seul `.exe` sur le PC Windows.
- `ios/` — app iOS native (SwiftUI) : nom, aperçu, chemin, date de modification.

## Ce que fait le serveur

| Geste | Effet sur le PC |
| --- | --- |
| Garder | déplacé dans `Validé\`, arborescence conservée (`Source\a\b\x.jpg` → `Validé\a\b\x.jpg`) |
| Supprimer | déplacé dans `Supprimé\`, arborescence conservée — rien n'est effacé, tu vides ce dossier toi-même |
| Passer | renvoyé en fin de file |
| Annuler | remet le dernier fichier à sa place |

Un fichier existant n'est jamais écrasé (`x (2).jpg`). Chaque déplacement est tracé dans `fileswiper-journal.csv`.

**Doublons** : comparaison par taille, puis SHA-256 des 64 premiers Ko, puis SHA-256 complet — entre les fichiers à trier *et* ceux déjà dans `Validé`. La carte affiche « Déjà dans Validé » / « N autres copies », et l'onglet Doublons permet de garder une copie et supprimer les autres en un tap.

## Serveur (Windows)

Récupérer `fileswiper-server.exe` (artefact du workflow CI, ou `go build` dans `server/`), puis :

```bash
fileswiper-server.exe -source "D:\A trier"
```

Options : `-validated`, `-trash` (défaut : `Validé` et `Supprimé` à côté de la source), `-port` (8787), `-token`.
La config est enregistrée dans `fileswiper.json` : les lancements suivants se font sans argument.

Au démarrage, le serveur affiche l'adresse et le code à saisir dans l'app. Autoriser l'accès au réseau privé quand le pare-feu Windows le demande.

## App iOS

Ouvrir `ios/FileSwiper.xcodeproj` (Xcode 16+, iOS 17+), choisir son équipe dans *Signing & Capabilities*, lancer sur l'iPhone.
Dans Réglages : adresse (`192.168.x.x:8787`) et code. iPhone et PC sur le même réseau.

## Dev

```bash
cd server && go test ./...
```
