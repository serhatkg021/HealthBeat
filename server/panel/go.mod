// Bu dosya Go kodu içermez: panel (React/Vite) bu dizinde durur ve `server` Go modülünün `./...` taramasına
// girmemelidir (node_modules içindeki bazı paketler .go dosyası taşır ve `go vet ./...` bozulur).
// İç içe bir go.mod bu alt ağacı server modülünden ayırır.
module healthbeat-panel

go 1.22
