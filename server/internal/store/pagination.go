package store

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
