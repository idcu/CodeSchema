package parser

import "github.com/idcu/codeschema/contrib/adapterx"

// ToAdapterX 将内部归一化 IR 转换为对外发布契约的 IRDocument（A 级适配器聚合包桥接）。
//
// 当前仓库内部以 internal/parser.IRDocument 为运行契约；对外发布时消费方以
// adapterx.IRDocument 为准。桥接层提供双向转换，保证「内部实现 ↔ 对外契约」
// 字段完全对齐（见 contrib/adapterx README 的发布说明）。
func ToAdapterX(doc *IRDocument) *adapterx.IRDocument {
	if doc == nil {
		return nil
	}
	out := &adapterx.IRDocument{
		Source:       doc.Source,
		Language:     doc.Language,
		FilePath:     doc.FilePath,
		FileHash:     doc.FileHash,
		CommitHash:   doc.CommitHash,
		LineCount:    doc.LineCount,
		ByteSize:     doc.ByteSize,
		ReferencedBy: append([]string(nil), doc.ReferencedBy...),
		Imports:      append([]string(nil), doc.Imports...),
	}
	for _, c := range doc.Classes {
		out.Classes = append(out.Classes, adapterx.ClassIR{
			Name: c.Name, FullName: c.FullName, Type: c.Type,
			ParentFQNs: append([]string(nil), c.ParentFQNs...),
			StartLine: c.StartLine, StartCol: c.StartCol, EndLine: c.EndLine, EndCol: c.EndCol,
			Modifier: c.Modifier, Doc: c.Doc,
			Annotations: append([]string(nil), c.Annotations...),
			Extra:       c.Extra,
		})
	}
	for _, m := range doc.Methods {
		out.Methods = append(out.Methods, adapterx.MethodIR{
			Name: m.Name, Signature: m.Signature, ReturnType: m.ReturnType,
			ClassFQN: m.ClassFQN,
			StartLine: m.StartLine, StartCol: m.StartCol, EndLine: m.EndLine, EndCol: m.EndCol,
			Modifier: m.Modifier, Doc: m.Doc,
			Annotations: append([]string(nil), m.Annotations...),
			IsStatic: m.IsStatic, IsAbstract: m.IsAbstract, IsConstructor: m.IsConstructor,
			Extra: m.Extra,
		})
		if len(m.Params) > 0 {
			for _, p := range m.Params {
				out.Methods[len(out.Methods)-1].Params = append(
					out.Methods[len(out.Methods)-1].Params,
					adapterx.ParamIR{Name: p.Name, Type: p.Type, Index: p.Index, Annotation: p.Annotation},
				)
			}
		}
	}
	for _, cl := range doc.Calls {
		out.Calls = append(out.Calls, adapterx.CallIR{
			CallerFQN: cl.CallerFQN, CalleeFQN: cl.CalleeFQN,
			CallType: cl.CallType, LineNumber: cl.LineNumber,
		})
	}
	return out
}

// FromAdapterX 将对外契约的 IRDocument 转回内部归一化 IR（与 ToAdapterX 互逆）。
func FromAdapterX(doc *adapterx.IRDocument) *IRDocument {
	if doc == nil {
		return nil
	}
	out := &IRDocument{
		Source:       doc.Source,
		Language:     doc.Language,
		FilePath:     doc.FilePath,
		FileHash:     doc.FileHash,
		CommitHash:   doc.CommitHash,
		LineCount:    doc.LineCount,
		ByteSize:     doc.ByteSize,
		ReferencedBy: append([]string(nil), doc.ReferencedBy...),
		Imports:      append([]string(nil), doc.Imports...),
	}
	for _, c := range doc.Classes {
		out.Classes = append(out.Classes, ClassIR{
			Name: c.Name, FullName: c.FullName, Type: c.Type,
			ParentFQNs: append([]string(nil), c.ParentFQNs...),
			StartLine: c.StartLine, StartCol: c.StartCol, EndLine: c.EndLine, EndCol: c.EndCol,
			Modifier: c.Modifier, Doc: c.Doc,
			Annotations: append([]string(nil), c.Annotations...),
			Extra:       c.Extra,
		})
	}
	for _, m := range doc.Methods {
		out.Methods = append(out.Methods, MethodIR{
			Name: m.Name, Signature: m.Signature, ReturnType: m.ReturnType,
			ClassFQN: m.ClassFQN,
			StartLine: m.StartLine, StartCol: m.StartCol, EndLine: m.EndLine, EndCol: m.EndCol,
			Modifier: m.Modifier, Doc: m.Doc,
			Annotations: append([]string(nil), m.Annotations...),
			IsStatic: m.IsStatic, IsAbstract: m.IsAbstract, IsConstructor: m.IsConstructor,
			Extra: m.Extra,
		})
		if len(m.Params) > 0 {
			for _, p := range m.Params {
				out.Methods[len(out.Methods)-1].Params = append(
					out.Methods[len(out.Methods)-1].Params,
					ParamIR{Name: p.Name, Type: p.Type, Index: p.Index, Annotation: p.Annotation},
				)
			}
		}
	}
	for _, cl := range doc.Calls {
		out.Calls = append(out.Calls, CallIR{
			CallerFQN: cl.CallerFQN, CalleeFQN: cl.CalleeFQN,
			CallType: cl.CallType, LineNumber: cl.LineNumber,
		})
	}
	return out
}
