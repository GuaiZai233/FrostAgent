import type { ComponentType } from '@angular/cdk/portal';
import { inject, Injectable, type TemplateRef } from '@angular/core';
import {
  type BrnDialogOptions,
  type BrnDialogRef,
  BrnDialogService,
  cssClassesToArray,
} from '@spartan-ng/brain/dialog';
import { HlmDialogContent } from './hlm-dialog-content';
import { hlmDialogOverlayClass } from './hlm-dialog-overlay';

export type HlmDialogOptions<
  DialogContext extends object = Record<string, never>,
> = BrnDialogOptions & {
  contentClass?: string;
  showCloseButton?: boolean;
  context?: DialogContext;
};

@Injectable({
  providedIn: 'root',
})
export class HlmDialogService {
  private readonly _brnDialogService = inject(BrnDialogService);

  public open<
    TResult = unknown,
    TContext extends object = Record<string, never>,
  >(
    component: ComponentType<unknown> | TemplateRef<unknown>,
    options?: Partial<HlmDialogOptions<TContext>>,
  ): BrnDialogRef<TResult> {
    const mergedOptions = {
      ...(options ?? {}),
      backdropClass: cssClassesToArray(
        `${hlmDialogOverlayClass} ${options?.backdropClass ?? ''}`,
      ),
      context: {
        ...(options?.context ?? {}),
        $component: component,
        $dynamicComponentClass: options?.contentClass,
        $showCloseButton: options?.showCloseButton,
      },
    };

    return this._brnDialogService.open<unknown, TResult>(
      HlmDialogContent,
      undefined,
      mergedOptions.context,
      mergedOptions,
    );
  }
}
