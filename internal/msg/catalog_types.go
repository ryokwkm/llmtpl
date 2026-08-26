package msg

// StateMsg は internal/state の文言。
type StateMsg struct {
	CannotRead  string // 引数: パス, err
	Corrupt     string // 引数: パス, err
	NewerFormat string // 引数: パス, 読んだ version, 対応 version
}

// JSONMsg は internal/mergejson の文言。
type JSONMsg struct {
	NotJSON       string // 引数: ラベル, err
	NotObject     string // 引数: ラベル
	FormatFailed  string // 引数: err
	TypeMismatch  string // 引数: ラベル, キー, 既存の型, 断片の型
	CannotCompare string // 引数: err
	TypeObject    string
	TypeArray     string
	TypeScalar    string
}

// RenderMsg は internal/render の文言。
type RenderMsg struct {
	NoTmplPath          string
	EmptySlotName       string
	PartialsGlobFailed  string // 引数: err
	PartialsParseFailed string // 引数: err
	SourceParseFailed   string // 引数: テンプレパス, err
	TemplateParseFailed string // 引数: err
	ExecFailed          string // 引数: err
}

// FileOutMsg は internal/fileout の文言。
type FileOutMsg struct {
	ArchiveFailed          string // 引数: err
	MkdirParentFailed      string // 引数: 生成先, err
	TempCreateFailed       string // 引数: 生成先, err
	TempWriteFailed        string // 引数: 一時ファイル名, err
	TempCloseFailed        string // 引数: 一時ファイル名, err
	ChmodFailed            string // 引数: 一時ファイル名, err
	ReplaceFailed          string // 引数: 生成先, err
	NoArchiveDir           string // 引数: ラベル
	ArchiveDirCreateFailed string // 引数: err
	ArchiveNamesExhausted  string // 引数: 退避先ディレクトリ, ベース名
}

// LinkMsg は internal/link の文言。
type LinkMsg struct {
	CannotUnlink       string // 引数: リンクパス, err
	WouldArchive       string
	CannotLink         string // 引数: リンクパス, err
	CannotArchive      string // 引数: パス, err
	CannotRead         string // 引数: パス, err
	ConflictNote       string // 引数: バンドル名, 相対パス, 先に提供したバンドル名
	LayerOutsideOutDir string // 引数: 層のパス
}

// FlagsMsg は internal/flags の文言。
type FlagsMsg struct {
	Section           string // 引数: conf パス, 行, 行の内容
	NoEquals          string // 引数: conf パス, 行, 行の内容
	EmptyKey          string // 引数: conf パス, 行, 行の内容
	BoolOnly          string // 引数: conf パス, 行, キー, 値
	ReadFailed        string // 引数: conf パス, err
	EmptyValue        string // 引数: conf パス, 行, キー
	PathKeyNotBool    string // 引数: conf パス, 行, キー, キー, 値
	TildeExpandFailed string // 引数: conf パス, 行, キー, err
	TildeUserForm     string // 引数: conf パス, 行, キー, 値
	NoBundles         string
	UnknownFlag       string // 引数: 出典, 未知のフラグ, 利用可能なフラグ
}

// BundleMsg は internal/bundle の文言。
type BundleMsg struct {
	CannotRead      string // 引数: パス, err
	CannotReadRoot  string // 引数: err
	LayoutViolation string // 引数: バンドル名, ディレクトリ名, 既定オーバーレイ, ディレクトリ名
	UnknownMetaKey  string // 引数: パス, 行, キー
	NoColon         string // 引数: 行, 行の内容
	EmptyKey        string // 引数: 行, 行の内容
}

// ApplyMsg は internal/apply の文言。
type ApplyMsg struct {
	ReservedBundleName     string // 引数: バンドルのパス
	BundleRootInDefaults   string // 引数: conf パス, 行, キー, ファイル名, 環境変数名, ターゲットの conf 名
	UnknownSlot            string // 引数: テンプレパス, slot 名, 有効なバンドル名
	TargetUnderRoot        string // 引数: ディレクトリ, バンドルルート
	NotReadAtThisPath      string // 引数: 見つけたパス, あるべきパス
	IsOverlayLayer         string // 引数: ディレクトリ, 既定オーバーレイ
	NotGeneratedUnderLayer string // 引数: テンプレ, 層, 置くべき名前, パス
	SkippedUnsupported     string
	TmplScanFailed         string // 引数: err
	HomeOrder              string // 引数: 環境変数名, ターゲット conf 名, 予約キー, バンドルディレクトリ名
	ConfHomeConflict       string // 引数: キー, 出典 A, 値 A, 出典 B, 値 B
	HomeLabelEnv           string // 引数: 環境変数名
	HomeLabelConf          string // 引数: conf の出典, 予約キー
	HomeLabelWalkUp        string
	ExplicitHomeMissing    string // 引数: 指定されたパス
	EnvHomeMissing         string // 引数: 環境変数名, 値
	ConfHomeMissing        string // 引数: conf の出典, キー, 値
	HomeNotFound           string // 引数: 解決順
	TargetScanFailed       string // 引数: err
}

// CmdMsg は CLI（main）の文言。--help とレポート表示。
type CmdMsg struct {
	Short          string
	Long           string // 引数: 解決順
	StaleGenerated string // 引数: 差分件数
	FlagTplHome    string
	FlagDryRun     string
	FlagVerbose    string
	ApplyShort     string
	CheckShort     string
	StatusShort    string
	BundlesShort   string
	DroppedTargets string // 引数: 除外件数
	NoTargets      string // 引数: 探索したディレクトリ, ターゲットの目印ファイル名
	TargetCount    string // 引数: 件数
	BundleRootLine string // 引数: バンドルルート, 出典
	OnNone         string
	Skip           string // 引数: 生成先, 理由
	Diff           string // 引数: 生成先
	Generated      string // 引数: 生成先
	Unchanged      string // 引数: 生成先
	ArchivedEntity string // 引数: 退避先
	ArchivedHint   string
	WouldArchive   string
	Orphan         string // 引数: バンドル名, 断片名, 断片名
	LinkPlanned    string // 引数: リンクパス, リンク先
	Linked         string // 引数: リンクパス, リンク先
	UnlinkPlanned  string // 引数: リンクパス
	Unlinked       string // 引数: リンクパス
	LinkArchived   string // 引数: 備考
	LinkConflict   string // 引数: 備考
	LinkKept       string // 引数: リンクパス
	NoBundles      string
	ColTarget      string
	ColBundle      string
	ColContents    string
	ColDesc        string
}
