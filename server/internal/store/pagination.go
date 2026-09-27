package store

import "strings"

// ListParams, panelin sayfalanabilir/aranabilir listelerinde (kullanıcılar, bir organizasyonun
// sunucuları, alert'ler) paylaşılır. Limit == 0, sayfalama istenmediği anlamına gelir: çağıran
// tüm satırları alır (geriye dönük uyumluluk — mevcut çağıranlar hiçbir şey değiştirmeden
// derlenmeye devam eder). Search, ilgili metin sütun(lar)ına göre büyük/küçük harf duyarsız bir
// alt dize eşleşmesidir; boşsa filtre uygulanmaz.
type ListParams struct {
	Search string
	Limit  int
	Offset int
}

// likeEscaper, LIKE/ILIKE desenindeki özel karakterleri (\ % _) PostgreSQL'in varsayılan kaçış karakteri \ ile kaçışlar.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// SearchPattern, Search'ün ILIKE deseniyle "içinde geçer" aramasıdır: kullanıcının yazdığı % ve _ joker değil, düz
// karakter olarak aranır.
func (p ListParams) SearchPattern() string {
	return "%" + likeEscaper.Replace(p.Search) + "%"
}
