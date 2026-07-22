package im

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatIMCitationTagsNumbersBooksByFirstAppearance(t *testing.T) {
	input := `第一句。<kb doc="Cataract_Surgery-Adi_Abulafia_2022_weknora.mhtml" chunk_id="chunk-a" /> ` +
		`第二句。<kb doc="Pediatric_Ophthalmology.pdf" chunk_id="chunk-b" /> ` +
		`第三句。<kb doc="Cataract_Surgery-Adi_Abulafia_2022_weknora.mhtml" chunk_id="chunk-c" />`

	want := "第一句。[1] 第二句。[2] 第三句。[1]\n\n" +
		"**参考资料**\n" +
		"[1] 《Cataract Surgery-Adi Abulafia 2022》\n" +
		"[2] 《Pediatric Ophthalmology》"

	assert.Equal(t, want, formatIMCitationTags(input))
}

func TestFormatIMCitationTagsCollapsesAdjacentBookDuplicates(t *testing.T) {
	input := `结论。<kb doc="Book_A_weknora.mhtml" chunk_id="chunk-a" />` +
		`<kb doc="Book_A_weknora.mhtml" chunk_id="chunk-b" /> ` +
		`<kb doc="Book_B.mht" chunk_id="chunk-c" />` +
		`<web url="https://example.com" />`

	want := "结论。[1] [2]\n\n" +
		"**参考资料**\n" +
		"[1] 《Book A》\n" +
		"[2] 《Book B》"

	assert.Equal(t, want, formatIMCitationTags(input))
}

func TestFormatIMCitationTagsCleansEscapedTitleAndPath(t *testing.T) {
	input := `结论。<kb doc="folder/A &amp; B_[Guide]_weknora.MHTML" chunk_id="chunk-a" />`

	want := "结论。[1]\n\n**参考资料**\n[1] 《A & B \\[Guide\\]》"
	assert.Equal(t, want, formatIMCitationTags(input))
}

func TestFormatIMCitationTagsLeavesUncitedContentUntouched(t *testing.T) {
	input := "普通回答，没有引用。\n"
	assert.Equal(t, input, formatIMCitationTags(input))
}

func TestIMDisplayPreparerOnlyAddsSourcesOnFinalUpdate(t *testing.T) {
	preparer := &imDisplayPreparer{
		resolver: newIMFileServiceResolver(nil, nil, nil, nil),
	}
	input := `结论。<kb doc="Book_weknora.mhtml" chunk_id="chunk-a" />`

	intermediate, _ := preparer.prepare(context.Background(), input, false)
	final, _ := preparer.prepare(context.Background(), input, true)

	assert.Equal(t, "结论。", intermediate)
	assert.Equal(t, "结论。[1]\n\n**参考资料**\n[1] 《Book》", final)
}
