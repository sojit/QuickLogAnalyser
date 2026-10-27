package config

// Limits are proposed MVP defaults from BUILD_SPEC — measure before claiming.
type Limits struct {
	MaxUploadBytes       int64
	MaxNestingDepth      int
	MaxMembers           int
	MaxExpandedBytes     int64
	MaxEvidenceBytes     int64
	MaxMemberTextBytes   int64
	MaxLineBytes         int
	MaxEventBytes        int
	PageDefault          int
	PageMax              int
	ContextLinesMax      int
	ContextBytesMax      int
	SearchQueryMaxBytes  int
	TotalAppDataBudget   int64
	ProgressMaxHz        int
}

func DefaultLimits() Limits {
	return Limits{
		MaxUploadBytes:      2 << 30, // 2 GiB
		MaxNestingDepth:     3,
		MaxMembers:          100_000,
		MaxExpandedBytes:    20 << 30,
		MaxEvidenceBytes:    10 << 30,
		MaxMemberTextBytes:  2 << 30,
		MaxLineBytes:        1 << 20,
		MaxEventBytes:       4 << 20,
		PageDefault:         50,
		PageMax:             200,
		ContextLinesMax:     200,
		ContextBytesMax:     256 << 10,
		SearchQueryMaxBytes: 1024,
		TotalAppDataBudget:  30 << 30,
		ProgressMaxHz:       4,
	}
}
