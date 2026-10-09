package assetmeta

type AssetInfo struct {
	Symbol   string `json:"symbol"`
	FullName string `json:"full_name"`
}

type Provider interface {
	GetAssetInfo(symbol string) (AssetInfo, bool)
}
