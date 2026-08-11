package feishu

import "testing"

func TestNormalizeFeishuBoldLabelSpacing_ClinicalFramework(t *testing.T) {
	input := "**第1～5天：**甲泼尼龙1 g/d静脉冲击；\n" +
		"**重症或恢复不佳：**尽早增加血浆置换5～7次；\n" +
		"**冲击后：**泼尼松约1 mg/kg/d口服；\n" +
		"**若已复发≥2次：**启动长期方案；\n" +
		"**传统方案仍复发：**重新核实诊断。"
	want := "**第1～5天：** 甲泼尼龙1 g/d静脉冲击；\n" +
		"**重症或恢复不佳：** 尽早增加血浆置换5～7次；\n" +
		"**冲击后：** 泼尼松约1 mg/kg/d口服；\n" +
		"**若已复发≥2次：** 启动长期方案；\n" +
		"**传统方案仍复发：** 重新核实诊断。"

	if got := normalizeFeishuBoldLabelSpacing(input); got != want {
		t.Fatalf("normalized clinical framework = %q, want %q", got, want)
	}
}

func TestNormalizeFeishuBoldLabelSpacing_LeadingBoldSummarySentence(t *testing.T) {
	input := "**简化为一句话：SHRM有血流、积液或出血，就按活动性nAMD足量抗VEGF；SHRM只是稳定的纤维瘢痕且无渗出，则以观察和定期OCT/OCTA监测为主。**具体应根据SHRM形态制定方案。"
	want := "**简化为一句话：SHRM有血流、积液或出血，就按活动性nAMD足量抗VEGF；SHRM只是稳定的纤维瘢痕且无渗出，则以观察和定期OCT/OCTA监测为主。** 具体应根据SHRM形态制定方案。"

	if got := normalizeFeishuBoldLabelSpacing(input); got != want {
		t.Fatalf("normalized summary sentence = %q, want %q", got, want)
	}
}

func TestNormalizeFeishuBoldLabelSpacing_OptionalListMarkers(t *testing.T) {
	input := "- **阶段一：**治疗A\n" +
		"  + **阶段二:**治疗B\n" +
		"1. **阶段三：**治疗C"
	want := "- **阶段一：** 治疗A\n" +
		"  + **阶段二:** 治疗B\n" +
		"1. **阶段三：** 治疗C"

	if got := normalizeFeishuBoldLabelSpacing(input); got != want {
		t.Fatalf("normalized list labels = %q, want %q", got, want)
	}
}

func TestNormalizeFeishuBoldLabelSpacing_PreservesUnrelatedMarkdown(t *testing.T) {
	input := "# 标题\n\n" +
		"普通的 **粗体内容** 保持不变。\n" +
		"正文中的**标签：**正文不处理。\n" +
		"**独立标题**\n下一段。\n" +
		"**药物**：剂量保持原来的中文标点位置。\n" +
		"**已经规范：** 正文不重复添加空格。\n" +
		"[**链接标签：**正文](https://example.com)\n" +
		"![**图片标签：**正文](img_test)"

	if got := normalizeFeishuBoldLabelSpacing(input); got != input {
		t.Fatalf("unrelated Markdown changed:\n got: %q\nwant: %q", got, input)
	}
}

func TestNormalizeFeishuBoldLabelSpacing_ProtectsCode(t *testing.T) {
	input := "`**行内示例：**正文`\n\n" +
		"```markdown\n**代码块示例：**正文\n```\n\n" +
		"**真实标签：**正文"
	want := "`**行内示例：**正文`\n\n" +
		"```markdown\n**代码块示例：**正文\n```\n\n" +
		"**真实标签：** 正文"

	if got := normalizeFeishuBoldLabelSpacing(input); got != want {
		t.Fatalf("code protection failed:\n got: %q\nwant: %q", got, want)
	}
}

func TestNormalizeFeishuBoldLabelSpacing_IsIdempotent(t *testing.T) {
	input := "**第1～5天：**甲泼尼龙。"
	once := normalizeFeishuBoldLabelSpacing(input)
	if twice := normalizeFeishuBoldLabelSpacing(once); twice != once {
		t.Fatalf("normalization is not idempotent: once=%q twice=%q", once, twice)
	}
}

func TestNormalizeFeishuMarkdownCompatibility_CombinesMathAndBoldFixes(t *testing.T) {
	input := "**成人剂量：**\\[8\\text{mg/kg，每4周1次}\\]"
	want := "**成人剂量：** 8 mg/kg，每4周1次"

	if got := normalizeFeishuMarkdownCompatibility(input); got != want {
		t.Fatalf("combined compatibility output = %q, want %q", got, want)
	}
}
