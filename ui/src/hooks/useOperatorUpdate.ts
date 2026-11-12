import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useSnackbar } from "@/contexts/SnackbarContext";
import { type Operator, updateOperator } from "@/queries/operators";
import { operatorBody } from "@/utils/operators";

export function useOperatorUpdate(
  operator: Operator,
  message: string,
  onSaved?: (saved: Operator) => void,
) {
  const queryClient = useQueryClient();
  const { showSnackbar } = useSnackbar();

  return useMutation({
    mutationFn: (change: Partial<Operator>) =>
      updateOperator(operator.name, operatorBody(operator, change)),
    onSuccess: (_result, change) => {
      void queryClient.invalidateQueries({ queryKey: ["operators"] });
      showSnackbar(message, "success");
      onSaved?.(operatorBody(operator, change));
    },
  });
}
