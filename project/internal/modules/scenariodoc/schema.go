package scenariodoc

import (
	"encoding/json"
	"time"
)

var topLevelKeys = []string{
	"format", "passport", "brief", "opponent", "issues", "state", "facts",
	"custom_moves", "evidence_weights", "move_effects", "start_stage",
	"stages", "end_stages", "wrapup_stage", "finals", "difficulty",
	"jailbreak", "process_criteria", "authoring",
}

// decodeDocument разбирает произвольный JSON в Document, собирая все
// найденные проблемы структуры (FR-SC-15: неизвестное поле — ошибка
// схемы). Никогда не паникует и не останавливается на первой проблеме:
// на месте нераспознанной части документа остаётся нулевое значение,
// а разбор продолжается — иначе автор увидел бы только одну ошибку за
// попытку сохранения.
func decodeDocument(document []byte) (*Document, []Diagnostic) {
	top, ok := asObject(json.RawMessage(document))
	if !ok {
		return &Document{}, []Diagnostic{{
			Severity: SeverityError, Rule: ruleSchema, Path: "",
			Message: "Документ должен быть JSON-объектом.",
		}}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(top, topLevelKeys, "", ruleSchema)...)

	doc := &Document{}
	if format, ok := asString(top["format"]); ok {
		doc.Format = format
		if format != "arena-scenario/1" {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleSchema, Path: ptr("format"),
				Message: "Неизвестная версия формата документа — ожидается «arena-scenario/1».",
			})
		}
	} else {
		diags = append(diags, missingField("", ruleSchema, "format"))
	}

	if raw, present := top["passport"]; present {
		p, d := decodePassport(raw, ptr("passport"))
		doc.Passport = p
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "passport"))
	}

	if raw, present := top["brief"]; present {
		b, d := decodeBrief(raw, ptr("brief"))
		doc.Brief = b
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "brief"))
	}

	if raw, present := top["opponent"]; present {
		o, d := decodeOpponent(raw, ptr("opponent"))
		doc.Opponent = o
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "opponent"))
	}

	if raw, present := top["issues"]; present {
		issues, d := decodeIssues(raw, ptr("issues"))
		doc.Issues = issues
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "issues"))
	}
	issueTypes := make(map[string]IssueType, len(doc.Issues))
	for _, is := range doc.Issues {
		if is.ID != "" {
			issueTypes[is.ID] = is.Type
		}
	}

	if raw, present := top["state"]; present {
		s, d := decodeState(raw, ptr("state"))
		doc.State = s
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "state"))
	}

	if raw, present := top["facts"]; present {
		facts, d := decodeFacts(raw, ptr("facts"))
		doc.Facts = facts
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "facts"))
	}

	if raw, present := top["custom_moves"]; present {
		cm, d := decodeCustomMoves(raw, ptr("custom_moves"))
		doc.CustomMoves = cm
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "custom_moves"))
	}

	if raw, present := top["evidence_weights"]; present {
		ew, d := decodeEvidenceWeights(raw, ptr("evidence_weights"))
		doc.EvidenceWeights = ew
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "evidence_weights"))
	}

	if raw, present := top["move_effects"]; present {
		me, d := decodeMoveEffects(raw, ptr("move_effects"))
		doc.MoveEffects = me
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "move_effects"))
	}

	if s, ok := asString(top["start_stage"]); ok {
		doc.StartStage = s
	} else {
		diags = append(diags, missingField("", ruleSchema, "start_stage"))
	}

	if raw, present := top["stages"]; present {
		stages, d := decodeStages(raw, ptr("stages"), issueTypes)
		doc.Stages = stages
		diags = append(diags, d...)
		if len(stages) < 2 {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleSchema, Path: ptr("stages"),
				Message: "Нужно не меньше двух этапов.",
			})
		}
	} else {
		diags = append(diags, missingField("", ruleSchema, "stages"))
	}

	if raw, present := top["end_stages"]; present {
		es, d := decodeEndStages(raw, ptr("end_stages"))
		doc.EndStages = es
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "end_stages"))
	}

	if s, ok := asString(top["wrapup_stage"]); ok {
		doc.WrapupStage = s
	} else {
		diags = append(diags, missingField("", ruleSchema, "wrapup_stage"))
	}

	if raw, present := top["finals"]; present {
		finals, d := decodeFinals(raw, ptr("finals"))
		doc.Finals = finals
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "finals"))
	}

	if raw, present := top["difficulty"]; present {
		diff, d := decodeDifficulty(raw, ptr("difficulty"), issueTypes)
		doc.Difficulty = diff
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "difficulty"))
	}

	if raw, present := top["jailbreak"]; present {
		jb, d := decodeJailbreak(raw, ptr("jailbreak"))
		doc.Jailbreak = jb
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "jailbreak"))
	}

	doc.ProcessCriteria = optionalString(top, "process_criteria")
	if doc.ProcessCriteria == "" {
		diags = append(diags, missingField("", ruleSchema, "process_criteria"))
	}

	if raw, present := top["authoring"]; present {
		a, d := decodeAuthoring(raw, ptr("authoring"))
		doc.Authoring = a
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField("", ruleSchema, "authoring"))
	}

	return doc, diags
}

// --- passport ---

var passportKeys = []string{"id", "version", "title", "mode", "sphere", "negotiation_type", "tags", "origin"}

func decodePassport(raw json.RawMessage, path string) (PassportSection, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return PassportSection{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, passportKeys, path, ruleSchema)...)

	p := PassportSection{}
	id, d := requireString(obj, "id", path, ruleSchema)
	diags = append(diags, d...)
	p.ID = id
	if v, d := requireInt(obj, "version", path, ruleSchema); len(d) == 0 {
		p.Version = v
		if v < 0 {
			diags = append(diags, wrongType(path, ruleSchema, "version", "числом не меньше 0"))
		}
	} else {
		diags = append(diags, d...)
	}
	title, d := requireString(obj, "title", path, ruleSchema)
	diags = append(diags, d...)
	p.Title = title

	mode, d := requireString(obj, "mode", path, ruleSchema)
	diags = append(diags, d...)
	p.Mode = Mode(mode)
	if len(d) == 0 && p.Mode != ModeTraining && p.Mode != ModeAssessment {
		diags = append(diags, wrongType(path, ruleSchema, "mode", "«training» или «assessment»"))
	}

	sphere, d := requireString(obj, "sphere", path, ruleSchema)
	diags = append(diags, d...)
	p.Sphere = Sphere(sphere)
	if len(d) == 0 && !validSphere(p.Sphere) {
		diags = append(diags, wrongType(path, ruleSchema, "sphere", "одной из перечисленных сфер"))
	}

	nt, d := requireString(obj, "negotiation_type", path, ruleSchema)
	diags = append(diags, d...)
	p.NegotiationType = NegotiationType(nt)
	if len(d) == 0 && !validNegotiationType(p.NegotiationType) {
		diags = append(diags, wrongType(path, ruleSchema, "negotiation_type", "«distributive», «integrative» или «mixed»"))
	}

	tags, d := requireStringArray(obj, "tags", path, ruleSchema)
	diags = append(diags, d...)
	p.Tags = tags

	origin, d := requireString(obj, "origin", path, ruleSchema)
	diags = append(diags, d...)
	p.Origin = Origin(origin)
	if len(d) == 0 && !validOrigin(p.Origin) {
		diags = append(diags, wrongType(path, ruleSchema, "origin", "«brief», «template», «copy» или «manual»"))
	}

	return p, diags
}

func validSphere(s Sphere) bool {
	switch s {
	case SphereProcurement, SphereInternalPromotion, SphereInternalClient, SphereSales,
		SphereContractorDeadline, SphereResourceSplit, SphereOther:
		return true
	default:
		return false
	}
}

func validNegotiationType(t NegotiationType) bool {
	return t == NegotiationDistributive || t == NegotiationIntegrative || t == NegotiationMixed
}

func validOrigin(o Origin) bool {
	switch o {
	case OriginBrief, OriginTemplate, OriginCopy, OriginManual:
		return true
	default:
		return false
	}
}

// --- brief ---

var briefKeys = []string{
	"situation", "participant_role", "goal", "constraints", "fallback_option",
	"known_facts", "time_limit_minutes", "hints",
}

func decodeBrief(raw json.RawMessage, path string) (Brief, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Brief{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, briefKeys, path, ruleSchema)...)

	b := Brief{}
	situation, d := requireString(obj, "situation", path, ruleSchema)
	diags = append(diags, d...)
	b.Situation = situation
	participantRole, d := requireString(obj, "participant_role", path, ruleSchema)
	diags = append(diags, d...)
	b.ParticipantRole = participantRole
	goal, d := requireString(obj, "goal", path, ruleSchema)
	diags = append(diags, d...)
	b.Goal = goal
	constraints, d := requireStringArray(obj, "constraints", path, ruleSchema)
	diags = append(diags, d...)
	b.Constraints = constraints
	fallbackOption, d := requireString(obj, "fallback_option", path, ruleSchema)
	diags = append(diags, d...)
	b.FallbackOption = fallbackOption
	knownFacts, d := requireStringArray(obj, "known_facts", path, ruleSchema)
	diags = append(diags, d...)
	b.KnownFacts = knownFacts
	if v, d := requireInt(obj, "time_limit_minutes", path, ruleSchema); len(d) == 0 {
		b.TimeLimitMinutes = v
		if v < 3 || v > 60 {
			diags = append(diags, wrongType(path, ruleSchema, "time_limit_minutes", "от 3 до 60"))
		}
	} else {
		diags = append(diags, d...)
	}
	hints, d := requireStringArray(obj, "hints", path, ruleSchema)
	diags = append(diags, d...)
	b.Hints = hints
	return b, diags
}

// --- opponent ---

var opponentKeys = []string{"card", "brief"}

func decodeOpponent(raw json.RawMessage, path string) (Opponent, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Opponent{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, opponentKeys, path, ruleSchema)...)

	o := Opponent{}
	if r, present := obj["card"]; present {
		c, d := decodeOpponentCard(r, ptrChild(path, "card"))
		o.Card = c
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField(path, ruleSchema, "card"))
	}
	if r, present := obj["brief"]; present {
		b, d := decodeOpponentBrief(r, ptrChild(path, "brief"))
		o.Brief = b
		diags = append(diags, d...)
	} else {
		diags = append(diags, missingField(path, ruleSchema, "brief"))
	}
	return o, diags
}

var cardKeys = []string{"name", "role", "organization", "speech_manner", "voice", "appearance"}

func decodeOpponentCard(raw json.RawMessage, path string) (OpponentCard, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return OpponentCard{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, cardKeys, path, ruleSchema)...)

	c := OpponentCard{}
	name, d := requireString(obj, "name", path, ruleSchema)
	diags = append(diags, d...)
	c.Name = name
	role, d := requireString(obj, "role", path, ruleSchema)
	diags = append(diags, d...)
	c.Role = role
	organization, d := requireString(obj, "organization", path, ruleSchema)
	diags = append(diags, d...)
	c.Organization = organization
	speechManner, d := requireString(obj, "speech_manner", path, ruleSchema)
	diags = append(diags, d...)
	c.SpeechManner = speechManner

	voicePath := ptrChild(path, "voice")
	if r, present := obj["voice"]; present {
		vObj, ok := asObject(r)
		if !ok {
			diags = append(diags, wrongTypeAt(voicePath, ruleSchema, "объектом"))
		} else {
			diags = append(diags, checkKnownKeys(vObj, []string{"voice_id", "speed"}, voicePath, ruleSchema)...)
			voiceID, d := requireString(vObj, "voice_id", voicePath, ruleSchema)
			diags = append(diags, d...)
			c.Voice.VoiceID = voiceID
			if v, d := requireNumber(vObj, "speed", voicePath, ruleSchema); len(d) == 0 {
				c.Voice.Speed = v
				if v < 0.5 || v > 2 {
					diags = append(diags, wrongType(voicePath, ruleSchema, "speed", "от 0.5 до 2"))
				}
			} else {
				diags = append(diags, d...)
			}
		}
	} else {
		diags = append(diags, missingField(path, ruleSchema, "voice"))
	}

	appearancePath := ptrChild(path, "appearance")
	if r, present := obj["appearance"]; present {
		aObj, ok := asObject(r)
		if !ok {
			diags = append(diags, wrongTypeAt(appearancePath, ruleSchema, "объектом"))
		} else {
			diags = append(diags, checkKnownKeys(aObj, []string{"avatar_id"}, appearancePath, ruleSchema)...)
			avatarID, d := requireString(aObj, "avatar_id", appearancePath, ruleSchema)
			diags = append(diags, d...)
			c.Appearance.AvatarID = avatarID
		}
	} else {
		diags = append(diags, missingField(path, ruleSchema, "appearance"))
	}

	return c, diags
}

var opponentBriefKeys = []string{
	"character", "goals", "interests", "fallback_option", "never_concede",
	"concede_for", "what_offends", "unknown_answer",
}

func decodeOpponentBrief(raw json.RawMessage, path string) (OpponentBrief, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return OpponentBrief{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, opponentBriefKeys, path, ruleSchema)...)

	b := OpponentBrief{}
	character, d := requireString(obj, "character", path, ruleSchema)
	diags = append(diags, d...)
	b.Character = character
	goals, d := requireStringArray(obj, "goals", path, ruleSchema)
	diags = append(diags, d...)
	b.Goals = goals

	interestsPath := ptrChild(path, "interests")
	if r, present := obj["interests"]; present {
		arr, ok := asArray(r)
		if !ok {
			diags = append(diags, wrongTypeAt(interestsPath, ruleSchema, "списком"))
		} else {
			if len(arr) < 1 {
				diags = append(diags, Diagnostic{
					Severity: SeverityError, Rule: ruleSchema, Path: interestsPath,
					Message: "Нужен хотя бы один интерес оппонента.",
				})
			}
			for i, el := range arr {
				interest, d := decodeInterest(el, ptrIndex(interestsPath, i))
				diags = append(diags, d...)
				b.Interests = append(b.Interests, interest)
			}
		}
	} else {
		diags = append(diags, missingField(path, ruleSchema, "interests"))
	}

	fallbackOption, d := requireString(obj, "fallback_option", path, ruleSchema)
	diags = append(diags, d...)
	b.FallbackOption = fallbackOption
	nc, d := requireStringArray(obj, "never_concede", path, ruleSchema)
	diags = append(diags, d...)
	b.NeverConcede = nc
	cf, d := requireStringArray(obj, "concede_for", path, ruleSchema)
	diags = append(diags, d...)
	b.ConcedeFor = cf
	whatOffends, d := requireString(obj, "what_offends", path, ruleSchema)
	diags = append(diags, d...)
	b.WhatOffends = whatOffends
	unknownAnswer, d := requireString(obj, "unknown_answer", path, ruleSchema)
	diags = append(diags, d...)
	b.UnknownAnswer = unknownAnswer
	return b, diags
}

var interestKeys = []string{"id", "label", "weight", "description"}

func decodeInterest(raw json.RawMessage, path string) (Interest, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Interest{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, interestKeys, path, ruleSchema)...)

	in := Interest{}
	id, d := requireString(obj, "id", path, ruleSchema)
	diags = append(diags, d...)
	in.ID = id
	label, d := requireString(obj, "label", path, ruleSchema)
	diags = append(diags, d...)
	in.Label = label
	if v, d := requireNumber(obj, "weight", path, ruleSchema); len(d) == 0 {
		in.Weight = v
		if v < 0.35 || v > 1 {
			diags = append(diags, wrongType(path, ruleSchema, "weight", "от 0.35 до 1"))
		}
	} else {
		diags = append(diags, d...)
	}
	description, d := requireString(obj, "description", path, ruleSchema)
	diags = append(diags, d...)
	in.Description = description
	return in, diags
}

// --- issues ---

var issueKeys = []string{
	"id", "label", "type", "unit", "direction", "options", "limits_depend_on",
	"revealed_by_fact", "participant", "opponent",
}

func decodeIssues(raw json.RawMessage, path string) ([]Issue, []Diagnostic) {
	arr, ok := asArray(raw)
	if !ok {
		return nil, []Diagnostic{wrongTypeAt(path, ruleSchema, "списком предметов торга")}
	}
	var diags []Diagnostic
	issues := make([]Issue, 0, len(arr))
	for i, el := range arr {
		is, d := decodeIssue(el, ptrIndex(path, i))
		diags = append(diags, d...)
		issues = append(issues, is)
	}
	return issues, diags
}

func decodeIssue(raw json.RawMessage, path string) (Issue, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Issue{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, issueKeys, path, ruleSchema)...)

	is := Issue{}
	id, d := requireString(obj, "id", path, ruleSchema)
	diags = append(diags, d...)
	is.ID = id
	label, d := requireString(obj, "label", path, ruleSchema)
	diags = append(diags, d...)
	is.Label = label
	typ, d := requireString(obj, "type", path, ruleSchema)
	diags = append(diags, d...)
	is.Type = IssueType(typ)
	if len(d) == 0 && is.Type != IssueNumber && is.Type != IssueDate && is.Type != IssueChoice {
		diags = append(diags, wrongType(path, ruleSchema, "type", "«number», «date» или «choice»"))
	}

	switch is.Type {
	case IssueNumber:
		unit, d := requireString(obj, "unit", path, ruleSchema)
		diags = append(diags, d...)
		is.Unit = unit
		dir, d := requireString(obj, "direction", path, ruleSchema)
		diags = append(diags, d...)
		is.Direction = Direction(dir)
	case IssueDate:
		dir, d := requireString(obj, "direction", path, ruleSchema)
		diags = append(diags, d...)
		is.Direction = Direction(dir)
	case IssueChoice:
		optionsPath := ptrChild(path, "options")
		if r, present := obj["options"]; present {
			arr, ok := asArray(r)
			if !ok {
				diags = append(diags, wrongTypeAt(optionsPath, ruleSchema, "списком вариантов"))
			} else {
				if len(arr) < 2 {
					diags = append(diags, Diagnostic{
						Severity: SeverityError, Rule: ruleSchema, Path: optionsPath,
						Message: "У предмета-выбора нужно не меньше двух вариантов.",
					})
				}
				for i, el := range arr {
					opt, d := decodeOption(el, ptrIndex(optionsPath, i))
					diags = append(diags, d...)
					is.Options = append(is.Options, opt)
				}
			}
		} else {
			diags = append(diags, missingField(path, ruleSchema, "options"))
		}
	}
	if is.Direction != "" && is.Direction != DirectionLowerBetter && is.Direction != DirectionHigherBetter {
		diags = append(diags, wrongType(path, ruleSchema, "direction", "«lower_better» или «higher_better»"))
	}

	is.LimitsDependOn = optionalString(obj, "limits_depend_on")
	is.RevealedByFact = optionalString(obj, "revealed_by_fact")

	participantPath := ptrChild(path, "participant")
	if r, present := obj["participant"]; present {
		side, d := decodeParticipantSide(r, participantPath, is.Type)
		diags = append(diags, d...)
		is.Participant = side
	} else {
		diags = append(diags, missingField(path, ruleSchema, "participant"))
	}

	opponentPath := ptrChild(path, "opponent")
	if r, present := obj["opponent"]; present {
		side, d := decodeOpponentSide(r, opponentPath, is.Type)
		diags = append(diags, d...)
		is.Opponent = side
	} else {
		diags = append(diags, missingField(path, ruleSchema, "opponent"))
	}

	return is, diags
}

func decodeOption(raw json.RawMessage, path string) (Option, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Option{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом {id, label}")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, []string{"id", "label"}, path, ruleSchema)...)
	opt := Option{}
	id, d := requireString(obj, "id", path, ruleSchema)
	diags = append(diags, d...)
	opt.ID = id
	label, d := requireString(obj, "label", path, ruleSchema)
	diags = append(diags, d...)
	opt.Label = label
	return opt, diags
}

func decodeParticipantSide(raw json.RawMessage, path string, issueType IssueType) (ParticipantSide, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return ParticipantSide{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, []string{"target", "limit", "acceptable"}, path, ruleSchema)...)
	side := ParticipantSide{}

	if r, present := obj["target"]; present {
		v, d := decodeIssueValue(r, ptrChild(path, "target"), issueType)
		diags = append(diags, d...)
		side.Target = v
	} else {
		diags = append(diags, missingField(path, ruleSchema, "target"))
	}

	switch issueType {
	case IssueNumber, IssueDate:
		if r, present := obj["limit"]; present {
			lim, d := decodeLimit(r, ptrChild(path, "limit"), issueType)
			diags = append(diags, d...)
			side.Limit = lim
		} else {
			diags = append(diags, missingField(path, ruleSchema, "limit"))
		}
	case IssueChoice:
		acc, d := requireStringArray(obj, "acceptable", path, ruleSchema)
		diags = append(diags, d...)
		side.Acceptable = acc
	}

	return side, diags
}

func decodeOpponentSide(raw json.RawMessage, path string, issueType IssueType) (OpponentSide, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return OpponentSide{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, []string{"start", "limit", "acceptable"}, path, ruleSchema)...)
	side := OpponentSide{}

	if r, present := obj["start"]; present {
		v, d := decodeIssueValue(r, ptrChild(path, "start"), issueType)
		diags = append(diags, d...)
		side.Start = v
	} else {
		diags = append(diags, missingField(path, ruleSchema, "start"))
	}

	switch issueType {
	case IssueNumber, IssueDate:
		if r, present := obj["limit"]; present {
			lim, d := decodeLimit(r, ptrChild(path, "limit"), issueType)
			diags = append(diags, d...)
			side.Limit = lim
		} else {
			diags = append(diags, missingField(path, ruleSchema, "limit"))
		}
	case IssueChoice:
		acc, d := requireStringArray(obj, "acceptable", path, ruleSchema)
		diags = append(diags, d...)
		side.Acceptable = acc
	}

	return side, diags
}

func decodeIssueValue(raw json.RawMessage, path string, issueType IssueType) (IssueValue, []Diagnostic) {
	switch issueType {
	case IssueNumber:
		n, ok := asNumber(raw)
		if !ok {
			return IssueValue{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "числом")}
		}
		return IssueValue{Kind: ValueNumber, Number: n}, nil
	case IssueDate:
		s, ok := asString(raw)
		if !ok || !isISODate(s) {
			return IssueValue{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "датой в формате ГГГГ-ММ-ДД")}
		}
		return IssueValue{Kind: ValueDate, Date: s}, nil
	case IssueChoice:
		s, ok := asString(raw)
		if !ok {
			return IssueValue{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "идентификатором варианта")}
		}
		return IssueValue{Kind: ValueOption, Option: s}, nil
	default:
		return IssueValue{}, nil
	}
}

func isISODate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func decodeLimit(raw json.RawMessage, path string, issueType IssueType) (Limit, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Limit{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом {value, by_option}")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, []string{"value", "by_option"}, path, ruleSchema)...)

	limit := Limit{}
	if r, present := obj["value"]; present {
		v, d := decodeIssueValue(r, ptrChild(path, "value"), issueType)
		diags = append(diags, d...)
		limit.Value = v
	} else {
		diags = append(diags, missingField(path, ruleSchema, "value"))
	}
	if r, present := obj["by_option"]; present {
		byOptObj, ok := asObject(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "by_option", "объектом «вариант → значение»"))
		} else {
			limit.ByOption = make(map[string]IssueValue, len(byOptObj))
			byOptPath := ptrChild(path, "by_option")
			for optID, vRaw := range byOptObj {
				v, d := decodeIssueValue(vRaw, ptrChild(byOptPath, optID), issueType)
				diags = append(diags, d...)
				limit.ByOption[optID] = v
			}
		}
	}
	return limit, diags
}

// --- state ---

var stateKeys = []string{"trust", "pressure", "credibility", "patience", "credit", "flags"}

func decodeState(raw json.RawMessage, path string) (State, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return State{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, stateKeys, path, ruleSchema)...)

	s := State{}
	s.Trust, diags = decodeBoundsInto(obj, "trust", path, diags)
	s.Pressure, diags = decodeBoundsInto(obj, "pressure", path, diags)
	s.Credibility, diags = decodeBoundsInto(obj, "credibility", path, diags)

	patiencePath := ptrChild(path, "patience")
	if r, present := obj["patience"]; present {
		pObj, ok := asObject(r)
		if !ok {
			diags = append(diags, wrongTypeAt(patiencePath, ruleSchema, "объектом"))
		} else {
			diags = append(diags, checkKnownKeys(pObj, []string{"initial", "per_reply"}, patiencePath, ruleSchema)...)
			initial, d := requireInt(pObj, "initial", patiencePath, ruleSchema)
			diags = append(diags, d...)
			s.Patience.Initial = initial
			if v, d := requireInt(pObj, "per_reply", patiencePath, ruleSchema); len(d) == 0 {
				s.Patience.PerReply = v
				if v < 0 || v > 10 {
					diags = append(diags, wrongType(patiencePath, ruleSchema, "per_reply", "от 0 до 10"))
				}
			} else {
				diags = append(diags, d...)
			}
		}
	} else {
		diags = append(diags, missingField(path, ruleSchema, "patience"))
	}

	creditPath := ptrChild(path, "credit")
	if r, present := obj["credit"]; present {
		cObj, ok := asObject(r)
		if !ok {
			diags = append(diags, wrongTypeAt(creditPath, ruleSchema, "объектом"))
		} else {
			diags = append(diags, checkKnownKeys(cObj, []string{"initial"}, creditPath, ruleSchema)...)
			if v, d := requireInt(cObj, "initial", creditPath, ruleSchema); len(d) == 0 {
				s.Credit.Initial = v
				if v < 0 {
					diags = append(diags, wrongType(creditPath, ruleSchema, "initial", "не меньше 0"))
				}
			} else {
				diags = append(diags, d...)
			}
		}
	} else {
		diags = append(diags, missingField(path, ruleSchema, "credit"))
	}

	flagsPath := ptrChild(path, "flags")
	if r, present := obj["flags"]; present {
		arr, ok := asArray(r)
		if !ok {
			diags = append(diags, wrongTypeAt(flagsPath, ruleSchema, "списком"))
		} else {
			for i, el := range arr {
				fd, d := decodeFlagDef(el, ptrIndex(flagsPath, i))
				diags = append(diags, d...)
				s.Flags = append(s.Flags, fd)
			}
		}
	} else {
		diags = append(diags, missingField(path, ruleSchema, "flags"))
	}

	return s, diags
}

func decodeBoundsInto(obj map[string]json.RawMessage, key, path string, diags []Diagnostic) (Bounds, []Diagnostic) {
	bPath := ptrChild(path, key)
	r, present := obj[key]
	if !present {
		return Bounds{}, append(diags, missingField(path, ruleSchema, key))
	}
	bObj, ok := asObject(r)
	if !ok {
		return Bounds{}, append(diags, wrongTypeAt(bPath, ruleSchema, "объектом {initial, min, max}"))
	}
	diags = append(diags, checkKnownKeys(bObj, []string{"initial", "min", "max"}, bPath, ruleSchema)...)
	b := Bounds{}
	initial, d := requireInt(bObj, "initial", bPath, ruleSchema)
	diags = append(diags, d...)
	b.Initial = initial
	min, d := requireInt(bObj, "min", bPath, ruleSchema)
	diags = append(diags, d...)
	b.Min = min
	max, d := requireInt(bObj, "max", bPath, ruleSchema)
	diags = append(diags, d...)
	b.Max = max
	for _, v := range []int{b.Initial, b.Min, b.Max} {
		if v < 0 || v > 100 {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleSchema, Path: bPath,
				Message: "Значения шкалы — целые числа от 0 до 100.",
			})
			break
		}
	}
	return b, diags
}

func decodeFlagDef(raw json.RawMessage, path string) (FlagDef, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return FlagDef{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом {id, label}")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, []string{"id", "label"}, path, ruleSchema)...)
	fd := FlagDef{}
	id, d := requireString(obj, "id", path, ruleSchema)
	diags = append(diags, d...)
	fd.ID = id
	label, d := requireString(obj, "label", path, ruleSchema)
	diags = append(diags, d...)
	fd.Label = label
	return fd, diags
}

// --- facts ---

var factKeys = []string{"id", "label", "text", "reveal_when", "style", "on_reveal"}

func decodeFacts(raw json.RawMessage, path string) ([]Fact, []Diagnostic) {
	arr, ok := asArray(raw)
	if !ok {
		return nil, []Diagnostic{wrongTypeAt(path, ruleSchema, "списком фактов")}
	}
	var diags []Diagnostic
	facts := make([]Fact, 0, len(arr))
	for i, el := range arr {
		f, d := decodeFact(el, ptrIndex(path, i))
		diags = append(diags, d...)
		facts = append(facts, f)
	}
	return facts, diags
}

func decodeFact(raw json.RawMessage, path string) (Fact, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Fact{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, factKeys, path, ruleSchema)...)

	f := Fact{}
	id, d := requireString(obj, "id", path, ruleSchema)
	diags = append(diags, d...)
	f.ID = id
	label, d := requireString(obj, "label", path, ruleSchema)
	diags = append(diags, d...)
	f.Label = label
	text, d := requireString(obj, "text", path, ruleSchema)
	diags = append(diags, d...)
	f.Text = text

	if r, present := obj["reveal_when"]; present {
		cond, d := decodeCondition(r, ptrChild(path, "reveal_when"))
		diags = append(diags, d...)
		f.RevealWhen = cond
	} else {
		diags = append(diags, missingField(path, ruleSchema, "reveal_when"))
	}

	style, d := requireString(obj, "style", path, ruleSchema)
	diags = append(diags, d...)
	f.Style = FactStyle(style)
	if len(d) == 0 && !validFactStyle(f.Style) {
		diags = append(diags, wrongType(path, ruleSchema, "style", "одной из перечисленных манер"))
	}

	if r, present := obj["on_reveal"]; present {
		orObj, ok := asObject(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "on_reveal", "объектом {change}"))
		} else {
			orPath := ptrChild(path, "on_reveal")
			diags = append(diags, checkKnownKeys(orObj, []string{"change"}, orPath, ruleSchema)...)
			change, d := decodeChange(orObj["change"], ptrChild(orPath, "change"))
			diags = append(diags, d...)
			f.OnReveal = &OnReveal{Change: change}
		}
	}

	return f, diags
}

func validFactStyle(s FactStyle) bool {
	switch s {
	case StyleOnDirectQuestion, StyleReluctant, StyleVolunteers, StyleAsArgument:
		return true
	default:
		return false
	}
}

// --- custom_moves ---

var customMoveKeys = []string{"id", "parent", "label", "description", "examples"}

func decodeCustomMoves(raw json.RawMessage, path string) ([]CustomMove, []Diagnostic) {
	arr, ok := asArray(raw)
	if !ok {
		return nil, []Diagnostic{wrongTypeAt(path, ruleSchema, "списком")}
	}
	var diags []Diagnostic
	moves := make([]CustomMove, 0, len(arr))
	for i, el := range arr {
		cm, d := decodeCustomMove(el, ptrIndex(path, i))
		diags = append(diags, d...)
		moves = append(moves, cm)
	}
	return moves, diags
}

func decodeCustomMove(raw json.RawMessage, path string) (CustomMove, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return CustomMove{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, customMoveKeys, path, ruleSchema)...)

	cm := CustomMove{}
	id, d := requireString(obj, "id", path, ruleSchema)
	diags = append(diags, d...)
	cm.ID = id
	parent, d := requireString(obj, "parent", path, ruleSchema)
	diags = append(diags, d...)
	cm.Parent = parent
	if cm.Parent != "" && !isBaseMove(cm.Parent) {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleSchema, Path: ptrChild(path, "parent"),
			Message: "Родитель собственного типа хода должен быть одним из базовых типов.",
		})
	}
	label, d := requireString(obj, "label", path, ruleSchema)
	diags = append(diags, d...)
	cm.Label = label
	description, d := requireString(obj, "description", path, ruleSchema)
	diags = append(diags, d...)
	cm.Description = description
	examples, d := requireStringArray(obj, "examples", path, ruleSchema)
	diags = append(diags, d...)
	cm.Examples = examples
	return cm, diags
}

// --- evidence_weights ---

func decodeEvidenceWeights(raw json.RawMessage, path string) (EvidenceWeights, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return EvidenceWeights{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, evidenceKeys, path, ruleSchema)...)

	values := make(map[string]float64, len(evidenceKeys))
	for _, key := range evidenceKeys {
		v, d := requireNumber(obj, key, path, ruleSchema)
		diags = append(diags, d...)
		if len(d) == 0 && (v < 0 || v > 1) {
			diags = append(diags, wrongType(path, ruleSchema, key, "от 0 до 1"))
		}
		values[key] = v
	}
	return EvidenceWeights{
		CompetitorQuote: values["competitor_quote"],
		MarketPrice:     values["market_price"],
		PastContract:    values["past_contract"],
		SpecDocument:    values["spec_document"],
		ThirdParty:      values["third_party"],
		PolicyRule:      values["policy_rule"],
		OwnConstraint:   values["own_constraint"],
		Opinion:         values["opinion"],
		None:            values["none"],
	}, diags
}

// --- move_effects ---

var moveEffectKeys = []string{"id", "move", "if", "once", "change", "set_flags", "clear_flags", "note"}

func decodeMoveEffects(raw json.RawMessage, path string) ([]MoveEffect, []Diagnostic) {
	arr, ok := asArray(raw)
	if !ok {
		return nil, []Diagnostic{wrongTypeAt(path, ruleSchema, "списком")}
	}
	var diags []Diagnostic
	effects := make([]MoveEffect, 0, len(arr))
	for i, el := range arr {
		me, d := decodeMoveEffect(el, ptrIndex(path, i))
		diags = append(diags, d...)
		effects = append(effects, me)
	}
	return effects, diags
}

func decodeMoveEffect(raw json.RawMessage, path string) (MoveEffect, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return MoveEffect{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, moveEffectKeys, path, ruleSchema)...)

	me := MoveEffect{}
	id, d := requireString(obj, "id", path, ruleSchema)
	diags = append(diags, d...)
	me.ID = id
	me.Move = optionalString(obj, "move")
	if r, present := obj["if"]; present {
		cond, d := decodeCondition(r, ptrChild(path, "if"))
		diags = append(diags, d...)
		me.If = &cond
	}
	me.Once = optionalBool(obj, "once")
	if r, present := obj["change"]; present {
		change, d := decodeChange(r, ptrChild(path, "change"))
		diags = append(diags, d...)
		me.Change = change
	} else {
		diags = append(diags, missingField(path, ruleSchema, "change"))
	}
	me.SetFlags = optionalStringArray(obj, "set_flags")
	me.ClearFlags = optionalStringArray(obj, "clear_flags")
	me.Note = optionalString(obj, "note")
	return me, diags
}

var changeKeys = []string{"trust", "pressure", "credibility", "patience", "credit"}

func decodeChange(raw json.RawMessage, path string) (Change, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Change{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, changeKeys, path, ruleSchema)...)
	c := Change{}
	if r, present := obj["trust"]; present {
		c.Trust, diags = decodeIntField(r, path, "trust", diags)
	}
	if r, present := obj["pressure"]; present {
		c.Pressure, diags = decodeIntField(r, path, "pressure", diags)
	}
	if r, present := obj["credibility"]; present {
		c.Credibility, diags = decodeIntField(r, path, "credibility", diags)
	}
	if r, present := obj["patience"]; present {
		c.Patience, diags = decodeIntField(r, path, "patience", diags)
	}
	if r, present := obj["credit"]; present {
		c.Credit, diags = decodeIntField(r, path, "credit", diags)
	}
	return c, diags
}

func decodeIntField(raw json.RawMessage, path, field string, diags []Diagnostic) (int, []Diagnostic) {
	v, integral, ok := asInt(raw)
	if !ok {
		return 0, append(diags, wrongType(path, ruleSchema, field, "числом"))
	}
	if !integral {
		diags = append(diags, wrongType(path, ruleSchema, field, "целым числом"))
	}
	return v, diags
}

// --- stages ---

var stageKeys = []string{
	"id", "label", "terminal", "directive", "offer_policy", "may_reveal",
	"fillers", "fallback_lines", "transitions", "timeout",
}

func decodeStages(raw json.RawMessage, path string, issueTypes map[string]IssueType) ([]Stage, []Diagnostic) {
	arr, ok := asArray(raw)
	if !ok {
		return nil, []Diagnostic{wrongTypeAt(path, ruleSchema, "списком этапов")}
	}
	var diags []Diagnostic
	stages := make([]Stage, 0, len(arr))
	for i, el := range arr {
		s, d := decodeStage(el, ptrIndex(path, i), issueTypes)
		diags = append(diags, d...)
		stages = append(stages, s)
	}
	return stages, diags
}

func decodeStage(raw json.RawMessage, path string, issueTypes map[string]IssueType) (Stage, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Stage{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, stageKeys, path, ruleSchema)...)

	s := Stage{}
	id, d := requireString(obj, "id", path, ruleSchema)
	diags = append(diags, d...)
	s.ID = id
	label, d := requireString(obj, "label", path, ruleSchema)
	diags = append(diags, d...)
	s.Label = label
	s.Terminal = optionalBool(obj, "terminal")

	directivePath := ptrChild(path, "directive")
	if r, present := obj["directive"]; present {
		d2, d := decodeDirective(r, directivePath)
		diags = append(diags, d...)
		s.Directive = d2
	} else {
		diags = append(diags, missingField(path, ruleSchema, "directive"))
	}

	if r, present := obj["offer_policy"]; present {
		arr, ok := asArray(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "offer_policy", "списком"))
		} else {
			opPath := ptrChild(path, "offer_policy")
			for i, el := range arr {
				op, d := decodeOfferPolicy(el, ptrIndex(opPath, i), issueTypes)
				diags = append(diags, d...)
				s.OfferPolicy = append(s.OfferPolicy, op)
			}
		}
	}

	s.MayReveal = optionalStringArray(obj, "may_reveal")

	fillers, d := requireStringArray(obj, "fillers", path, ruleSchema)
	diags = append(diags, d...)
	s.Fillers = fillers
	if len(d) == 0 && len(fillers) < 1 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleSchema, Path: ptrChild(path, "fillers"),
			Message: "Нужна хотя бы одна реплика-заполнитель.",
		})
	}

	fallbackLines, d := requireStringArray(obj, "fallback_lines", path, ruleSchema)
	diags = append(diags, d...)
	s.FallbackLines = fallbackLines
	if len(d) == 0 && len(fallbackLines) < 1 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleSchema, Path: ptrChild(path, "fallback_lines"),
			Message: "Нужна хотя бы одна запасная реплика.",
		})
	}

	if r, present := obj["transitions"]; present {
		arr, ok := asArray(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "transitions", "списком"))
		} else {
			trPath := ptrChild(path, "transitions")
			for i, el := range arr {
				tr, d := decodeTransition(el, ptrIndex(trPath, i))
				diags = append(diags, d...)
				s.Transitions = append(s.Transitions, tr)
			}
		}
	}

	if r, present := obj["timeout"]; present {
		toObj, ok := asObject(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "timeout", "объектом {after_replies, to}"))
		} else {
			toPath := ptrChild(path, "timeout")
			diags = append(diags, checkKnownKeys(toObj, []string{"after_replies", "to"}, toPath, ruleSchema)...)
			after, d := requireInt(toObj, "after_replies", toPath, ruleSchema)
			diags = append(diags, d...)
			to, d := requireString(toObj, "to", toPath, ruleSchema)
			diags = append(diags, d...)
			s.Timeout = &Timeout{AfterReplies: after, To: to}
		}
	}

	return s, diags
}

var directiveKeys = []string{"goal", "allowed_intents", "forbidden", "tactics"}

func decodeDirective(raw json.RawMessage, path string) (Directive, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Directive{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, directiveKeys, path, ruleSchema)...)
	d := Directive{}
	goal, dg := requireString(obj, "goal", path, ruleSchema)
	diags = append(diags, dg...)
	d.Goal = goal
	intents, dd := requireStringArray(obj, "allowed_intents", path, ruleSchema)
	diags = append(diags, dd...)
	d.AllowedIntents = intents
	if len(dd) == 0 && len(intents) < 1 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleSchema, Path: ptrChild(path, "allowed_intents"),
			Message: "Нужно хотя бы одно разрешённое намерение.",
		})
	}
	forbidden, dd := requireStringArray(obj, "forbidden", path, ruleSchema)
	diags = append(diags, dd...)
	d.Forbidden = forbidden
	tactics, dd := requireStringArray(obj, "tactics", path, ruleSchema)
	diags = append(diags, dd...)
	d.Tactics = tactics
	return d, diags
}

var offerPolicyKeys = []string{"issue", "can_move", "step", "stop_at", "requires_reciprocity", "unlock_cost"}

func decodeOfferPolicy(raw json.RawMessage, path string, issueTypes map[string]IssueType) (OfferPolicy, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return OfferPolicy{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, offerPolicyKeys, path, ruleSchema)...)

	op := OfferPolicy{}
	issue, d := requireString(obj, "issue", path, ruleSchema)
	diags = append(diags, d...)
	op.Issue = issue
	canMove, d := requireBool(obj, "can_move", path, ruleSchema)
	diags = append(diags, d...)
	op.CanMove = canMove
	issueType := issueTypes[op.Issue]

	if r, present := obj["step"]; present {
		v, ok := asNumber(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "step", "числом"))
		} else {
			op.Step, op.HasStep = v, true
		}
	}
	if r, present := obj["stop_at"]; present {
		lim, d := decodeLimit(r, ptrChild(path, "stop_at"), issueType)
		diags = append(diags, d...)
		op.StopAt, op.HasStopAt = lim, true
	}
	if r, present := obj["requires_reciprocity"]; present {
		v, ok := asBool(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "requires_reciprocity", "true/false"))
		} else {
			op.RequiresReciprocity, op.HasRequiresReciprocity = v, true
		}
	}
	if r, present := obj["unlock_cost"]; present {
		v, integral, ok := asInt(r)
		if !ok || !integral {
			diags = append(diags, wrongType(path, ruleSchema, "unlock_cost", "целым числом"))
		} else {
			op.UnlockCost, op.HasUnlockCost = v, true
		}
	}
	return op, diags
}

func decodeTransition(raw json.RawMessage, path string) (Transition, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Transition{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом {to, when}")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, []string{"to", "when"}, path, ruleSchema)...)
	tr := Transition{}
	to, d := requireString(obj, "to", path, ruleSchema)
	diags = append(diags, d...)
	tr.To = to
	if r, present := obj["when"]; present {
		cond, d := decodeCondition(r, ptrChild(path, "when"))
		diags = append(diags, d...)
		tr.When = cond
	} else {
		diags = append(diags, missingField(path, ruleSchema, "when"))
	}
	return tr, diags
}

func decodeEndStages(raw json.RawMessage, path string) (EndStages, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return EndStages{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом {deal, no_deal}")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, []string{"deal", "no_deal"}, path, ruleSchema)...)
	es := EndStages{}
	deal, d := requireString(obj, "deal", path, ruleSchema)
	diags = append(diags, d...)
	es.Deal = deal
	noDeal, d := requireString(obj, "no_deal", path, ruleSchema)
	diags = append(diags, d...)
	es.NoDeal = noDeal
	return es, diags
}

// --- finals ---

var finalKeys = []string{"id", "rank", "title", "when", "epilogue"}

func decodeFinals(raw json.RawMessage, path string) ([]Final, []Diagnostic) {
	arr, ok := asArray(raw)
	if !ok {
		return nil, []Diagnostic{wrongTypeAt(path, ruleSchema, "списком финалов")}
	}
	var diags []Diagnostic
	finals := make([]Final, 0, len(arr))
	for i, el := range arr {
		f, d := decodeFinal(el, ptrIndex(path, i))
		diags = append(diags, d...)
		finals = append(finals, f)
	}
	return finals, diags
}

func decodeFinal(raw json.RawMessage, path string) (Final, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Final{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, finalKeys, path, ruleSchema)...)

	f := Final{}
	id, d := requireString(obj, "id", path, ruleSchema)
	diags = append(diags, d...)
	f.ID = id
	rank, d := requireString(obj, "rank", path, ruleSchema)
	diags = append(diags, d...)
	f.Rank = Rank(rank)
	if len(d) == 0 && !validRank(f.Rank) {
		diags = append(diags, wrongType(path, ruleSchema, "rank", "одной из букв S, A, B, C, D, F"))
	}
	title, d := requireString(obj, "title", path, ruleSchema)
	diags = append(diags, d...)
	f.Title = title
	if r, present := obj["when"]; present {
		cond, d := decodeCondition(r, ptrChild(path, "when"))
		diags = append(diags, d...)
		f.When = cond
	} else {
		diags = append(diags, missingField(path, ruleSchema, "when"))
	}
	epilogue, d := requireString(obj, "epilogue", path, ruleSchema)
	diags = append(diags, d...)
	f.Epilogue = epilogue
	return f, diags
}

func validRank(r Rank) bool {
	switch r {
	case RankS, RankA, RankB, RankC, RankD, RankF:
		return true
	default:
		return false
	}
}

// --- difficulty ---

var difficultyKeys = []string{"easy", "hard"}

func decodeDifficulty(raw json.RawMessage, path string, issueTypes map[string]IssueType) (Difficulty, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Difficulty{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом {easy, hard}")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, difficultyKeys, path, ruleSchema)...)

	d := Difficulty{}
	if r, present := obj["easy"]; present {
		s, dd := decodeLevelSettings(r, ptrChild(path, "easy"), issueTypes)
		diags = append(diags, dd...)
		d.Easy = s
	} else {
		diags = append(diags, missingField(path, ruleSchema, "easy"))
	}
	if r, present := obj["hard"]; present {
		s, dd := decodeLevelSettings(r, ptrChild(path, "hard"), issueTypes)
		diags = append(diags, dd...)
		d.Hard = s
	} else {
		diags = append(diags, missingField(path, ruleSchema, "hard"))
	}
	return d, diags
}

var levelSettingsKeys = []string{
	"patience_initial", "trust_initial", "unlock_cost_factor", "penalty_factor",
	"issues", "fact_conditions", "fact_styles", "opponent_manner",
	"extra_tactics", "hints",
}

func decodeLevelSettings(raw json.RawMessage, path string, issueTypes map[string]IssueType) (LevelSettings, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return LevelSettings{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, levelSettingsKeys, path, ruleSchema)...)

	s := LevelSettings{}
	if r, present := obj["patience_initial"]; present {
		v, integral, ok := asInt(r)
		if !ok || !integral {
			diags = append(diags, wrongType(path, ruleSchema, "patience_initial", "целым числом"))
		} else {
			s.PatienceInitial, s.HasPatienceInitial = v, true
		}
	}
	if r, present := obj["trust_initial"]; present {
		v, integral, ok := asInt(r)
		if !ok || !integral || v < 0 || v > 100 {
			diags = append(diags, wrongType(path, ruleSchema, "trust_initial", "целым числом от 0 до 100"))
		} else {
			s.TrustInitial, s.HasTrustInitial = v, true
		}
	}
	if r, present := obj["unlock_cost_factor"]; present {
		v, ok := asNumber(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "unlock_cost_factor", "числом"))
		} else {
			s.UnlockCostFactor, s.HasUnlockCostFactor = v, true
		}
	}
	if r, present := obj["penalty_factor"]; present {
		v, ok := asNumber(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "penalty_factor", "числом"))
		} else {
			s.PenaltyFactor, s.HasPenaltyFactor = v, true
		}
	}

	if r, present := obj["issues"]; present {
		issObj, ok := asObject(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "issues", "объектом «предмет → настройки»"))
		} else {
			s.Issues = make(map[string]IssueDifficultyAdjust, len(issObj))
			issuesPath := ptrChild(path, "issues")
			for issueID, adjRaw := range issObj {
				adj, d := decodeIssueDifficultyAdjust(adjRaw, ptrChild(issuesPath, issueID), issueTypes[issueID])
				diags = append(diags, d...)
				s.Issues[issueID] = adj
			}
		}
	}

	if r, present := obj["fact_conditions"]; present {
		fcObj, ok := asObject(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "fact_conditions", "объектом «факт → условие»"))
		} else {
			s.FactConditions = make(map[string]Condition, len(fcObj))
			fcPath := ptrChild(path, "fact_conditions")
			for factID, condRaw := range fcObj {
				cond, d := decodeCondition(condRaw, ptrChild(fcPath, factID))
				diags = append(diags, d...)
				s.FactConditions[factID] = cond
			}
		}
	}

	if r, present := obj["fact_styles"]; present {
		fsObj, ok := asObject(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "fact_styles", "объектом «факт → манера»"))
		} else {
			s.FactStyles = make(map[string]FactStyle, len(fsObj))
			for factID, styleRaw := range fsObj {
				styleStr, ok := asString(styleRaw)
				if !ok || !validFactStyle(FactStyle(styleStr)) {
					diags = append(diags, wrongType(path, ruleSchema, "fact_styles", "одной из перечисленных манер"))
					continue
				}
				s.FactStyles[factID] = FactStyle(styleStr)
			}
		}
	}

	s.OpponentManner = optionalString(obj, "opponent_manner")
	s.ExtraTactics = optionalStringArray(obj, "extra_tactics")
	s.Hints = optionalStringArray(obj, "hints")

	return s, diags
}

func decodeIssueDifficultyAdjust(raw json.RawMessage, path string, issueType IssueType) (IssueDifficultyAdjust, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return IssueDifficultyAdjust{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, []string{"opponent_start", "limit_shift"}, path, ruleSchema)...)
	adj := IssueDifficultyAdjust{}
	if r, present := obj["opponent_start"]; present {
		if issueType == IssueChoice {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleDealZone1a, Path: ptrChild(path, "opponent_start"),
				Message: "У предмета-выбора старт оппонента менять нельзя — он всегда первый допустимый вариант.",
			})
		} else {
			v, d := decodeIssueValue(r, ptrChild(path, "opponent_start"), issueType)
			diags = append(diags, d...)
			adj.OpponentStart, adj.HasOpponentStart = &v, true
		}
	}
	if r, present := obj["limit_shift"]; present {
		v, ok := asNumber(r)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "limit_shift", "числом"))
		} else {
			adj.LimitShift, adj.HasLimitShift = v, true
		}
	}
	return adj, diags
}

// --- jailbreak ---

var jailbreakKeys = []string{"in_character_attempts", "in_character_hint", "escalation_stage", "end_after_attempts", "end_stage"}

func decodeJailbreak(raw json.RawMessage, path string) (Jailbreak, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Jailbreak{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, jailbreakKeys, path, ruleSchema)...)

	jb := Jailbreak{}
	if v, d := requireInt(obj, "in_character_attempts", path, ruleSchema); len(d) == 0 {
		jb.InCharacterAttempts = v
		if v < 2 {
			diags = append(diags, wrongType(path, ruleSchema, "in_character_attempts", "не меньше 2"))
		}
	} else {
		diags = append(diags, d...)
	}
	inCharacterHint, d := requireString(obj, "in_character_hint", path, ruleSchema)
	diags = append(diags, d...)
	jb.InCharacterHint = inCharacterHint
	escalationStage, d := requireString(obj, "escalation_stage", path, ruleSchema)
	diags = append(diags, d...)
	jb.EscalationStage = escalationStage
	if v, d := requireInt(obj, "end_after_attempts", path, ruleSchema); len(d) == 0 {
		jb.EndAfterAttempts = v
		if v < 4 {
			diags = append(diags, wrongType(path, ruleSchema, "end_after_attempts", "не меньше 4"))
		}
	} else {
		diags = append(diags, d...)
	}
	endStage, d := requireString(obj, "end_stage", path, ruleSchema)
	diags = append(diags, d...)
	jb.EndStage = endStage
	return jb, diags
}

// --- authoring ---

var authoringKeys = []string{"source_template", "copied_from", "brief", "notes"}

func decodeAuthoring(raw json.RawMessage, path string) (Authoring, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Authoring{}, []Diagnostic{wrongTypeAt(path, ruleSchema, "объектом")}
	}
	var diags []Diagnostic
	diags = append(diags, checkKnownKeys(obj, authoringKeys, path, ruleSchema)...)

	a := Authoring{}
	a.SourceTemplate = optionalString(obj, "source_template")
	if r, present := obj["copied_from"]; present {
		cfObj, ok := asObject(r)
		if ok {
			cfPath := ptrChild(path, "copied_from")
			diags = append(diags, checkKnownKeys(cfObj, []string{"scenario_id", "version"}, cfPath, ruleSchema)...)
			scenarioID, d := requireString(cfObj, "scenario_id", cfPath, ruleSchema)
			diags = append(diags, d...)
			version, d := requireInt(cfObj, "version", cfPath, ruleSchema)
			diags = append(diags, d...)
			a.CopiedFrom = &CopiedFrom{ScenarioID: scenarioID, Version: version}
		} else {
			diags = append(diags, wrongTypeAt(ptrChild(path, "copied_from"), ruleSchema, "объектом {scenario_id, version}"))
		}
	}
	if raw, present := obj["brief"]; present {
		s, ok := asString(raw)
		if !ok {
			diags = append(diags, wrongType(path, ruleSchema, "brief", "строкой"))
		}
		a.Brief = s
	} else {
		diags = append(diags, missingField(path, ruleSchema, "brief"))
	}
	notes := optionalString(obj, "notes")
	if len(notes) > 4000 {
		diags = append(diags, wrongType(path, ruleSchema, "notes", "не длиннее 4000 знаков"))
	}
	a.Notes = notes
	return a, diags
}
