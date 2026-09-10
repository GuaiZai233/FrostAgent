package content

// ImageSubType extracts the image subtype from segment data, checking both
// canonical "sub_type" (used by NapCat / OneBot 11) and camelCase "subType" (used by LuckyLillia).
func ImageSubType(data map[string]any) any {
	if data == nil {
		return nil
	}
	if v, ok := data["sub_type"]; ok && v != nil {
		return v
	}
	if v, ok := data["subType"]; ok && v != nil {
		return v
	}
	return nil
}

// NormalizeMessageSegment canonicalizes vendor-specific wire format differences
// into canonical representations while preserving vendor-specific fields for compatibility.
//
// In particular:
// - subType <-> sub_type: LuckyLillia uses camelCase "subType" while NapCat uses "sub_type".
// - emojiId / emojiPackageId <-> emoji_id / emoji_package_id: metadata normalization.
// - top-level struct fields (Url, FileSize, SubType) are mapped into Data if missing.
func NormalizeMessageSegment(seg MessageSegment) MessageSegment {
	if seg.Data == nil {
		seg.Data = make(map[string]any)
	}

	// 1. subtype normalization (subType <-> sub_type)
	if subType := ImageSubType(seg.Data); subType != nil {
		if seg.Data["sub_type"] == nil {
			seg.Data["sub_type"] = subType
		}
		if seg.Data["subType"] == nil {
			seg.Data["subType"] = subType
		}
	} else if seg.SubType != "" {
		seg.Data["sub_type"] = seg.SubType
		seg.Data["subType"] = seg.SubType
	}

	// 2. market face metadata normalization (emojiId <-> emoji_id, emojiPackageId <-> emoji_package_id)
	if emojiID := dataString(seg.Data, "emoji_id", "emojiId"); emojiID != "" {
		if seg.Data["emoji_id"] == nil {
			seg.Data["emoji_id"] = emojiID
		}
		if seg.Data["emojiId"] == nil {
			seg.Data["emojiId"] = emojiID
		}
	}
	if pkgID := dataString(seg.Data, "emoji_package_id", "emojiPackageId"); pkgID != "" {
		if seg.Data["emoji_package_id"] == nil {
			seg.Data["emoji_package_id"] = pkgID
		}
		if seg.Data["emojiPackageId"] == nil {
			seg.Data["emojiPackageId"] = pkgID
		}
	}

	// 3. url / path normalization
	if seg.Url != "" && seg.Data["url"] == nil {
		seg.Data["url"] = seg.Url
	}
	if seg.FileSize != 0 && seg.Data["file_size"] == nil {
		seg.Data["file_size"] = seg.FileSize
	}

	return seg
}

// NormalizeMessageSegments normalizes a slice of MessageSegment.
func NormalizeMessageSegments(segments []MessageSegment) []MessageSegment {
	if len(segments) == 0 {
		return segments
	}
	normalized := make([]MessageSegment, len(segments))
	for i, seg := range segments {
		normalized[i] = NormalizeMessageSegment(seg)
	}
	return normalized
}
