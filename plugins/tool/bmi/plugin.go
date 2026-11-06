// Package bmi 根据身高体重计算 BMI 并给出健康建议。
package bmi

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strconv"

	"shizukubot/sdk"
)

// 移植自 koishi-plugin-bmi-calculator，提示语取自其默认配置。
var advice = map[string][]string{
	"underweight": {
		"您的体重稍轻，请适度增加营养摄入。",
		"为了更好的健康，可以尝试多食用一些富含营养的食物。",
		"多摄入一些蛋白质和健康脂肪，有助于提升身体素质。",
		"健康增重需要时间，可以根据自身状况，合理适当增重。",
		"请适当增加饮食量吧，这会更有助于身体的发展哦~",
	},
	"normal": {
		"您的体重很标准，继续保持健康的生活方式吧！",
		"正常体重是身体健康的一项标志，继续保持良好的生活习惯。",
		"恭喜您保持了理想的体重，坚持下去会更加健康。",
		"继续维持正常体重吧！有益于心血管和全身健康哦~",
		"加油！继续保持正常体重！有效预防慢性疾病。",
	},
	"overweight": {
		"注意啦，您的体重是健康的，但身体已经不苗条了唔，适当锻炼很重要哦~",
		"控制饮食，适度运动，是改善体重的有效途径。",
		"每天保持适量的运动，有助于控制体重。",
		"建议逐渐减少高热量食物的摄入。",
		"合理的饮食结构对控制超重非常重要。",
	},
	"moderately_obese": {
		"建议调整饮食，并开始健康的锻炼计划。",
		"适度的有氧运动对减轻体重有积极的影响。",
		"控制每餐的食量，有助于逐渐减轻体重。",
		"定期测量体重，可以更好地掌握减肥的效果。",
		"保持心情愉快，对减肥效果也有积极作用。",
	},
	"severely_obese": {
		"请重视您的健康，定期咨询医生和制定减重计划。",
		"建议您了解适合自己的减肥方式，这会更有助于提高成功率哦~",
		"慢慢来，稳步减肥比急功近利更有益。",
		"注重饮食的多样性，有助于提高减肥效果。",
		"每天保持充足的水分摄入，对减肥有一定帮助。",
	},
	"very_severely_obese": {
		"非常建议您咨询医生，制定科学的饮食和锻炼方案。",
		"非常建议您逐渐减轻体重，您需要全方位的合理调整生活习惯。",
		"非常建议您寻找减肥的合适方式，这可以更好地保持身体健康。",
		"非常建议您与医生合作，制定个性化的减肥计划更为有效。",
		"请积极参与健康社群，非常建议您向他人学习减肥经验。",
	},
}

// Definition registers /bmi.
func Definition() sdk.Definition {
	return sdk.CommandPlugin(sdk.Meta{
		ID:          "bmi",
		Name:        "BMI 计算",
		Version:     "1.0.0",
		Description: "计算 BMI 并给出健康建议：/bmi 身高(cm) 体重(斤)",
	}, "bmi", Handle)
}

// Handle validates the two numeric arguments and replies BMI plus advice.
func Handle(ctx context.Context, c sdk.Call) error {
	if len(c.Args) < 2 {
		return c.Reply(ctx, "用法：/bmi 身高(cm) 体重(斤)，如 /bmi 175 145")
	}
	height, err1 := strconv.Atoi(c.Args[0])
	weight, err2 := strconv.Atoi(c.Args[1])
	if err1 != nil || err2 != nil {
		return c.Reply(ctx, "身高（cm）和体重（斤）应该输入数字，如：/bmi 175 145")
	}
	if height <= 0 || weight <= 0 {
		return c.Reply(ctx, "身高（cm）和体重（斤）都不能为0，请输入有效的数值。")
	}
	if height > 300 || weight > 1200 {
		return c.Reply(ctx, "输入数值超出人类可能范围，请检查后再试，例如：/bmi 170 120")
	}
	value := calculate(height, weight)
	if math.IsNaN(value) || value < 10 || value > 100 {
		return c.Reply(ctx, "计算出的 BMI 数值异常，请检查输入是否准确。")
	}
	return c.Reply(ctx, fmt.Sprintf("您的BMI指数为%.1f。\n%s", value, adviceFor(value)))
}

func calculate(height, weight int) float64 {
	kg := float64(weight) * 0.5
	m := float64(height) / 100
	return kg / (m * m)
}

func adviceFor(value float64) string {
	var key string
	switch {
	case value < 18.5:
		key = "underweight"
	case value < 24.9:
		key = "normal"
	case value < 29.9:
		key = "overweight"
	case value < 34.9:
		key = "moderately_obese"
	case value < 39.9:
		key = "severely_obese"
	default:
		key = "very_severely_obese"
	}
	list := advice[key]
	return list[rand.Intn(len(list))]
}
