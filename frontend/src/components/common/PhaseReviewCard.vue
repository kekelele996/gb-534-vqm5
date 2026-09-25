<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { CheckCheck, CircleCheck, SearchX, ShieldAlert } from 'lucide-vue-next'
import PhaseBadge from './PhaseBadge.vue'
import DeviationBadge from './DeviationBadge.vue'
import type { PhaseReview, PhaseScore } from '../../types/deviation-analysis'
import { phaseDecisionLabels, type PhaseDecision } from '../../types/enums/phase-decision'
import type { DeviationLevel } from '../../types/enums/deviation-level'
import { deviationLevelForScore } from '../../utils/deviation'

const props = defineProps<{
  score: PhaseScore
  review?: PhaseReview
  editable: boolean
  saving?: boolean
}>()
const emit = defineEmits<{ submit: [payload: { phase: string; decision: PhaseDecision; comment: string }] }>()

const level = computed<DeviationLevel>(() => deviationLevelForScore(props.score.weighted_deviation))
const decision = ref<PhaseDecision | ''>('')
const comment = ref('')

watch(
  () => props.review,
  (review) => {
    decision.value = (review?.decision as PhaseDecision) ?? ''
    comment.value = review?.comment ?? ''
  },
  { immediate: true },
)

const options: { value: PhaseDecision; label: string; icon: typeof CircleCheck }[] = [
  { value: 'accepted', label: phaseDecisionLabels.accepted, icon: CircleCheck },
  { value: 'follow_up', label: phaseDecisionLabels.follow_up, icon: SearchX },
  { value: 'false_alarm', label: phaseDecisionLabels.false_alarm, icon: ShieldAlert },
]

function submit() {
  if (!decision.value || !comment.value.trim()) return
  emit('submit', { phase: props.score.phase, decision: decision.value, comment: comment.value.trim() })
}
</script>

<template>
  <article class="phase-review-card" :class="{ concluded: review }">
    <header class="phase-review-head">
      <PhaseBadge :phase="score.phase" />
      <DeviationBadge :level="level" />
      <span v-if="review" class="concluded-tag"><CheckCheck :size="14" />已有结论</span>
    </header>
    <dl class="phase-review-metrics">
      <div><dt>加权偏差</dt><dd>{{ (score.weighted_deviation * 100).toFixed(1) }}%</dd></div>
      <div><dt>曲线距离</dt><dd>{{ score.curve_distance.toFixed(3) }}</dd></div>
      <div><dt>斜率</dt><dd>{{ score.slope_deviation.toFixed(3) }}</dd></div>
      <div><dt>峰值时刻</dt><dd>{{ score.peak_time_deviation.toFixed(3) }}</dd></div>
    </dl>

    <div v-if="review && !editable" class="phase-review-frozen">
      <strong>{{ phaseDecisionLabels[review.decision] }}</strong>
      <p>{{ review.comment }}</p>
      <small>{{ review.reviewed_by_name }} · {{ new Date(review.submitted_at).toLocaleString() }}</small>
    </div>
    <div v-else-if="!editable" class="phase-review-pending">
      <strong>待复核结论</strong>
      <p>该异常阶段尚未由复核人逐项处理。</p>
    </div>

    <template v-else>
      <div class="phase-review-options">
        <button
          v-for="option in options"
          :key="option.value"
          type="button"
          class="phase-decision-btn"
          :class="{ active: decision === option.value }"
          @click="decision = option.value"
        >
          <component :is="option.icon" :size="15" />{{ option.label }}
        </button>
      </div>
      <el-input
        v-model="comment"
        type="textarea"
        :rows="2"
        maxlength="500"
        show-word-limit
        placeholder="写一句说明（必填），例如：离线检测证实生长期溶氧确实偏低。"
      />
      <div class="phase-review-foot">
        <small v-if="review">原结论：{{ phaseDecisionLabels[review.decision] }} · {{ review.reviewed_by_name }} · {{ new Date(review.submitted_at).toLocaleString() }}</small>
        <span v-else />
        <el-button
          type="primary"
          size="small"
          :loading="saving"
          :disabled="!decision || !comment.trim()"
          @click="submit"
        >
          {{ review ? '更新该阶段结论' : '提交该阶段结论' }}
        </el-button>
      </div>
    </template>
  </article>
</template>
