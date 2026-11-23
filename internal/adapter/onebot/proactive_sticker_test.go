package onebot

import "testing"

func TestFilterProactiveStickerCQ(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"before[CQ:mface,emoji_id=synthetic]after", "beforeafter"},
		{"[CQ:sticker,id=synthetic]", ""},
		{"[CQ:image,file=sticker.gif,sub_type=1]", ""},
		{"[CQ:image,subType=1,file=sticker.png]", ""},
		{"[CQ:image,file=sticker.png,is_sticker=true]", ""},
		{"[CQ:image,file=photo.gif,sub_type=0]", "[CQ:image,file=photo.gif,sub_type=0]"},
		{"[CQ:image,file=photo.png]", "[CQ:image,file=photo.png]"},
		{"[CQ:face,id=1] hello", "[CQ:face,id=1] hello"},
		{"[CQ:image,file=a&#44;b.png]", "[CQ:image,file=a&#44;b.png]"},
		{"&#91;CQ:mface,id=synthetic&#93;", "&#91;CQ:mface,id=synthetic&#93;"},
		{`{"messages":[{"type":"plain","text":"[CQ:mface,id=synthetic]"}]}`, `{"messages":[{"type":"plain","text":"[CQ:mface,id=synthetic]"}]}`},
	} {
		if got := filterProactiveStickerCQ(tc.input); got != tc.want {
			t.Errorf("filterProactiveStickerCQ(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
