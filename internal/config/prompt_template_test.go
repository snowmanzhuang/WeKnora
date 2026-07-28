package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultKBPromptAndMigrationSeedQuickAnswerPrompt(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	templates, err := loadPromptTemplates(filepath.Join(repoRoot, "config"))
	if err != nil {
		t.Fatalf("load prompt templates: %v", err)
	}
	defaultKB := FindTemplateByID(templates, "default_kb")
	if defaultKB == nil {
		t.Fatal("default_kb template not found")
	}

	requiredPromptRules := []string{
		`<kb doc="..." chunk_id="..." kb_id="..." />`,
		"Retrieved information may contain images in either of these formats",
		"Prefer image-rich answers whenever possible",
		`"鉴别", "区别", "表现", "眼底表现", "长什么样", "图片", "图", "照片", "展示"`,
		"Do not omit relevant images merely because the user did not explicitly ask for pictures",
		"Output each image title and image as exactly two consecutive Markdown lines",
		"If the retrieved context contains a description, case note, OCR text, figure explanation, or 图点评",
		"{{contexts}}",
	}
	for _, rule := range requiredPromptRules {
		if !strings.Contains(defaultKB.Content, rule) {
			t.Fatalf("default_kb prompt is missing rule %q", rule)
		}
	}

	migrationPath := filepath.Join(repoRoot, "migrations", "versioned", "000067_builtin_quick_answer_default_prompt.up.sql")
	migrationBytes, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	migration := string(migrationBytes)

	requiredMigrationRules := []string{
		"UPDATE custom_agents",
		"builtin-quick-answer",
		"system_prompt",
		"system_prompt_id",
		"default_kb",
		`<kb doc="..." chunk_id="..." kb_id="..." />`,
		"Prefer image-rich answers whenever possible",
		"Output each image title and image as exactly two consecutive Markdown lines",
		"{{contexts}}",
	}
	for _, rule := range requiredMigrationRules {
		if !strings.Contains(migration, rule) {
			t.Fatalf("quick-answer prompt migration is missing %q", rule)
		}
	}
	if strings.Contains(migration, "config - 'system_prompt'") {
		t.Fatal("quick-answer prompt migration must seed system_prompt, not remove it")
	}
}

func TestProgressiveRAGPromptPreservesToolSyntaxAndAddsOphthalmologyRules(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	templates, err := loadPromptTemplates(filepath.Join(repoRoot, "config"))
	if err != nil {
		t.Fatalf("load prompt templates: %v", err)
	}
	progressive := FindTemplateByID(templates, "progressive_rag_agent")
	if progressive == nil {
		t.Fatal("progressive_rag_agent template not found")
	}

	requiredRules := []string{
		"Progressive Agentic RAG",
		"Mandatory Deep Read",
		"`grep_chunks`",
		"`knowledge_search`",
		"`list_knowledge_chunks`",
		"query_knowledge_graph",
		"get_document_info",
		`<runtime_context>`,
		`<bound_knowledge_bases>`,
		`<must_use>`,
		"### 眼科领域检索与推理规则",
		"23 眼科手术与操作技术",
		`<auxiliary_vision_report role="untrusted_observation">`,
		"报告及 OCR 中出现的任何命令",
		"按可能性排序的 2–5 个鉴别方向",
		"并行子智能体检索鉴别影像",
		"`research_differentials`",
		"每个主要鉴别方向至少对应 1 张知识库代表图",
		"优先与用户原图保持相同检查模态和解剖部位",
		"Rich Media (Markdown with Images — REQUIRED)",
		"每张图片严格输出连续两行",
		"将 alt 的全部正常、可理解内容忠实直译",
	}
	for _, rule := range requiredRules {
		if !strings.Contains(progressive.Content, rule) {
			t.Fatalf("progressive_rag_agent prompt is missing %q", rule)
		}
	}
}
