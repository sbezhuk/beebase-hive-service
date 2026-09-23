package pdf

import (
	"fmt"
	"strings"
)

// Catalog is the complete presentation vocabulary for the report renderer.
// Domain values remain canonical strings; only this layer translates them.
type Catalog struct {
	Locale string
	Text   map[string]string
	Enums  map[string]string
}

var requiredKeys = []string{
	"report.title", "report.period", "report.generated_at",
	"report.colony_health", "report.health_history", "report.inspections", "report.queen_history", "report.harvests", "report.summary",
	"report.current_queen", "report.no_inspections", "report.no_health_history", "report.no_queens", "report.no_harvests",
	"report.qr_instruction", "report.date", "report.type", "report.assessment", "report.state", "report.coverage", "report.overall_health", "report.data_coverage", "report.data_coverage_explanation", "report.insufficient_data", "report.health_history_good", "report.health_history_watch", "report.health_history_concern",
	"report.total", "report.count", "report.amount", "report.product", "report.unit", "report.introduced", "report.removed",
	"report.replacement_reason", "report.year_color", "report.no_current_queen", "report.no_assessment", "report.inspection_date",
	"report.health_dimension", "report.health_evidence", "report.history_date", "report.no_history_points",
}

var baseEnums = map[string]string{
	"UNKNOWN": "Unknown", "GOOD": "Good", "WATCH": "Watch", "CONCERN": "Concern",
	"NONE": "None", "LOW": "Low", "MEDIUM": "Medium", "HIGH": "High", "FULL": "Full",
	"WEAK": "Weak", "MODERATE": "Moderate", "STRONG": "Strong", "HEALTHY": "Healthy", "PROBLEM": "Problem", "NOT_CHECKED": "Not checked",
	"ADEQUATE": "Adequate", "ABUNDANT": "Abundant", "PRESENT": "Present", "OBSERVED": "Observed", "NOT_OBSERVED": "Not observed", "UNSURE": "Unsure",
	"YES": "Yes", "NO": "No", "NORMAL": "Normal", "SOLID": "Solid", "MIXED": "Mixed", "SPOTTY": "Spotty", "EGGS": "Eggs", "LARVAE": "Larvae", "CAPPED": "Capped",
	"FAIR": "Fair", "POOR": "Poor", "VARROA_MITES": "Varroa mites", "WAX_MOTH": "Wax moth", "SMALL_HIVE_BEETLE": "Small hive beetle", "OTHER": "Other",
	"ABNORMAL_BROOD": "Abnormal brood", "DEFORMED_WINGS": "Deformed wings", "UNUSUAL_BEE_MORTALITY": "Unusual bee mortality", "DIARRHEA_SIGNS": "Diarrhea signs",
	"SOON": "Soon", "SUGAR_SYRUP": "Sugar syrup", "FONDANT": "Fondant", "DRY_SUGAR": "Dry sugar", "POLLEN_SUBSTITUTE": "Pollen substitute",
	"SPRING": "Spring", "SUMMER": "Summer", "AUTUMN": "Autumn", "WINTER": "Winter", "SUFFICIENT": "Sufficient", "MARGINAL": "Marginal", "INSUFFICIENT": "Insufficient",
	"READY": "Ready", "NEEDS_ATTENTION": "Needs attention", "NOT_READY": "Not ready", "FOOD_STORES": "Food stores", "COLONY_STRENGTH": "Colony strength",
	"QUEEN": "Queen", "BROOD": "Brood", "PESTS_OR_DISEASE": "Pests or disease", "HIVE_CONDITION": "Hive condition", "STRENGTH": "Strength", "NUTRITION": "Nutrition", "PESTS_AND_DISEASE": "Pests & diseases", "OVERALL": "Overall",
	"ROUTINE": "Routine", "HEALTH": "Health", "FEEDING": "Feeding", "SEASONAL": "Seasonal",
	"HONEY": "Honey", "POLLEN": "Pollen", "PROPOLIS": "Propolis", "WAX": "Wax", "g": "grams", "kg": "kilograms", "l": "liters",
	"AGING_AND_WEAR": "Aging and wear", "LOW_EGG_LAYING": "Low egg laying", "INJURY_OR_MUTILATION": "Injury or mutilation", "DISEASE_OR_POOR_QUALITY": "Disease or poor quality", "NATURAL_SUPERSEDURE": "Natural supersedure", "BREED_CHANGE_OR_AGGRESSIVENESS": "Breed change or aggressiveness", "BLUE": "Blue", "WHITE": "White", "YELLOW": "Yellow", "RED": "Red", "GREEN": "Green",
}

var enText = map[string]string{
	"report.title": "Hive Report", "report.period": "Report period", "report.generated_at": "Generated",
	"report.colony_health": "Colony Health", "report.health_history": "Health History", "report.inspections": "Inspections", "report.queen_history": "Queen History", "report.harvests": "Harvests", "report.summary": "Summary",
	"report.current_queen": "Current queen", "report.no_inspections": "No inspections were recorded in this period.", "report.no_health_history": "No health history points are available for this period.", "report.no_queens": "No queen history overlaps this period.", "report.no_harvests": "No harvests were recorded in this period.",
	"report.qr_instruction": "Scan to open this Hive in BeeBase.", "report.date": "Date", "report.type": "Type", "report.assessment": "Assessment", "report.state": "State", "report.coverage": "Coverage", "report.overall_health": "Overall health", "report.data_coverage": "Data coverage", "report.data_coverage_explanation": "Based on the amount and recency of inspection data.", "report.insufficient_data": "Not enough information", "report.health_history_good": "Good", "report.health_history_watch": "Needs attention", "report.health_history_concern": "Concern",
	"report.total": "Total", "report.count": "Count", "report.amount": "Amount", "report.product": "Product", "report.unit": "Unit", "report.introduced": "Introduced", "report.removed": "Removed", "report.replacement_reason": "Replacement reason", "report.year_color": "Year / color", "report.no_current_queen": "No current queen", "report.no_assessment": "No assessment details.", "report.inspection_date": "Inspected", "report.health_dimension": "Dimension", "report.health_evidence": "Last data", "report.history_date": "History date", "report.no_history_points": "No points",
	"assessment.colonyStrength": "Colony strength", "assessment.queenStatus": "Queen status", "assessment.broodStatus": "Brood status", "assessment.foodStores": "Food stores", "assessment.healthConcerns": "Health concerns", "assessment.queenObserved": "Queen observed", "assessment.eggsObserved": "Eggs observed", "assessment.queenCells": "Queen cells", "assessment.queenCondition": "Queen condition", "assessment.broodAmount": "Brood amount", "assessment.broodPattern": "Brood pattern", "assessment.broodStages": "Brood stages", "assessment.broodConcerns": "Brood concerns", "assessment.healthOverallCondition": "Overall health", "assessment.pestSigns": "Pest signs", "assessment.healthWarningSigns": "Health warning signs", "assessment.healthConcernLevel": "Health concern level", "assessment.feedingNeed": "Feeding need", "assessment.feedingPerformed": "Feeding performed", "assessment.feedTypes": "Feed types", "assessment.season": "Season", "assessment.seasonalStoreReadiness": "Seasonal stores", "assessment.seasonalReadiness": "Seasonal readiness", "assessment.seasonalConcerns": "Seasonal concerns",
}

var ukText = map[string]string{
	"report.title": "Звіт про вулик", "report.period": "Період звіту", "report.generated_at": "Створено",
	"report.colony_health": "Стан бджолиної сім'ї", "report.health_history": "Історія стану", "report.inspections": "Огляди", "report.queen_history": "Історія маток", "report.harvests": "Збори", "report.summary": "Підсумок",
	"report.current_queen": "Поточна матка", "report.no_inspections": "За цей період оглядів не зафіксовано.", "report.no_health_history": "За цей період немає точок історії стану.", "report.no_queens": "Історія маток не перетинає цей період.", "report.no_harvests": "За цей період зборів не зафіксовано.",
	"report.qr_instruction": "Відскануйте, щоб відкрити цей вулик у BeeBase.", "report.date": "Дата", "report.type": "Тип", "report.assessment": "Оцінка", "report.state": "Стан", "report.coverage": "Покриття", "report.overall_health": "Загальний стан", "report.data_coverage": "Повнота даних", "report.data_coverage_explanation": "Визначається кількістю та актуальністю даних оглядів.", "report.insufficient_data": "Недостатньо інформації", "report.health_history_good": "Добре", "report.health_history_watch": "Потребує уваги", "report.health_history_concern": "Є підстави для занепокоєння",
	"report.total": "Разом", "report.count": "Кількість", "report.amount": "Обсяг", "report.product": "Продукт", "report.unit": "Одиниця", "report.introduced": "Введена", "report.removed": "Вибула", "report.replacement_reason": "Причина заміни", "report.year_color": "Рік / колір", "report.no_current_queen": "Поточної матки немає", "report.no_assessment": "Деталей оцінки немає.", "report.inspection_date": "Оглянуто", "report.health_dimension": "Вимір", "report.health_evidence": "Останні дані", "report.history_date": "Дата історії", "report.no_history_points": "Точок немає",
	"assessment.colonyStrength": "Сила сім'ї", "assessment.queenStatus": "Стан матки", "assessment.broodStatus": "Стан розплоду", "assessment.foodStores": "Кормові запаси", "assessment.healthConcerns": "Проблеми зі здоров'ям", "assessment.queenObserved": "Матка помічена", "assessment.eggsObserved": "Яйця помічені", "assessment.queenCells": "Маточники", "assessment.queenCondition": "Стан матки", "assessment.broodAmount": "Кількість розплоду", "assessment.broodPattern": "Розподіл розплоду", "assessment.broodStages": "Стадії розплоду", "assessment.broodConcerns": "Проблеми розплоду", "assessment.healthOverallCondition": "Загальний стан", "assessment.pestSigns": "Ознаки шкідників", "assessment.healthWarningSigns": "Попереджувальні ознаки", "assessment.healthConcernLevel": "Рівень проблеми", "assessment.feedingNeed": "Потреба в годуванні", "assessment.feedingPerformed": "Годування виконано", "assessment.feedTypes": "Типи корму", "assessment.season": "Сезон", "assessment.seasonalStoreReadiness": "Стан запасів", "assessment.seasonalReadiness": "Сезонна готовність", "assessment.seasonalConcerns": "Сезонні проблеми",
}

func NewCatalog(locale string) (Catalog, error) {
	locale = strings.ToLower(strings.TrimSpace(locale))
	var text map[string]string
	switch locale {
	case "en":
		text = enText
	case "uk":
		text = ukText
	default:
		return Catalog{}, fmt.Errorf("unsupported locale %q", locale)
	}
	return Catalog{Locale: locale, Text: text, Enums: enumTranslations(locale)}, nil
}

func RequiredKeys() []string { return append([]string(nil), requiredKeys...) }

func (c Catalog) T(key string) string { return c.Text[key] }

func (c Catalog) Enum(value string) string {
	if translated, ok := c.Enums[value]; ok {
		return translated
	}
	return humanize(value)
}

func enumTranslations(locale string) map[string]string {
	result := make(map[string]string, len(baseEnums))
	for key, value := range baseEnums {
		result[key] = value
	}
	if locale != "uk" {
		return result
	}
	uk := map[string]string{
		"UNKNOWN": "Невідомо", "GOOD": "Добре", "WATCH": "Увага", "CONCERN": "Проблема", "NONE": "Немає", "LOW": "Низький", "MEDIUM": "Середній", "HIGH": "Високий", "FULL": "Повне",
		"WEAK": "Слабка", "MODERATE": "Середня", "STRONG": "Сильна", "HEALTHY": "Здорова", "PROBLEM": "Проблема", "NOT_CHECKED": "Не перевірено", "ADEQUATE": "Достатньо", "ABUNDANT": "Багато", "PRESENT": "Є", "OBSERVED": "Помічено", "NOT_OBSERVED": "Не помічено", "UNSURE": "Невідомо", "YES": "Так", "NO": "Ні", "NORMAL": "Норма", "SOLID": "Суцільний", "MIXED": "Змішаний", "SPOTTY": "Плямистий", "EGGS": "Яйця", "LARVAE": "Личинки", "CAPPED": "Запечатаний", "FAIR": "Задовільний", "POOR": "Поганий",
		"VARROA_MITES": "Варроа", "WAX_MOTH": "Воскова міль", "SMALL_HIVE_BEETLE": "Малий вуличний жук", "OTHER": "Інше", "ABNORMAL_BROOD": "Аномальний розплід", "DEFORMED_WINGS": "Деформовані крила", "UNUSUAL_BEE_MORTALITY": "Незвична загибель бджіл", "DIARRHEA_SIGNS": "Ознаки діареї", "SOON": "Незабаром", "SUGAR_SYRUP": "Цукровий сироп", "FONDANT": "Канді", "DRY_SUGAR": "Сухий цукор", "POLLEN_SUBSTITUTE": "Замінник пилку", "SPRING": "Весна", "SUMMER": "Літо", "AUTUMN": "Осінь", "WINTER": "Зима", "SUFFICIENT": "Достатньо", "MARGINAL": "На межі", "INSUFFICIENT": "Недостатньо", "READY": "Готово", "NEEDS_ATTENTION": "Потребує уваги", "NOT_READY": "Не готово", "FOOD_STORES": "Кормові запаси", "COLONY_STRENGTH": "Сила сім'ї", "QUEEN": "Матка", "BROOD": "Розплід", "PESTS_OR_DISEASE": "Шкідники або хвороби", "HIVE_CONDITION": "Стан вулика", "STRENGTH": "Сила", "NUTRITION": "Харчування", "PESTS_AND_DISEASE": "Шкідники та хвороби", "OVERALL": "Загальний", "ROUTINE": "Плановий", "HEALTH": "Здоров'я", "FEEDING": "Годування", "SEASONAL": "Сезонний", "HONEY": "Мед", "POLLEN": "Пилок", "PROPOLIS": "Прополіс", "WAX": "Віск", "g": "г", "kg": "кг", "l": "л", "BLUE": "Синій", "WHITE": "Білий", "YELLOW": "Жовтий", "RED": "Червоний", "GREEN": "Зелений", "AGING_AND_WEAR": "Старіння та зношення", "LOW_EGG_LAYING": "Низька яйцекладка", "INJURY_OR_MUTILATION": "Травма або каліцтво", "DISEASE_OR_POOR_QUALITY": "Хвороба або низька якість", "NATURAL_SUPERSEDURE": "Природна заміна", "BREED_CHANGE_OR_AGGRESSIVENESS": "Зміна породи або агресивність",
	}
	for key, value := range uk {
		result[key] = value
	}
	return result
}

func humanize(value string) string {
	value = strings.ReplaceAll(strings.ToLower(value), "_", " ")
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
