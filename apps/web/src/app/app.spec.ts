import { TestBed } from '@angular/core/testing';
import { App } from './app';
import { DashboardService } from './dashboard.service';

describe('App', () => {
  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [App],
      providers: [
        {
          provide: DashboardService,
          useValue: {
            getOverview: () =>
              Promise.resolve({
                botName: 'FrostAgent',
                version: '0.1.0',
                uptimeSeconds: 12n,
                totalMessagesProcessed: 34n,
                activeSessions: 2,
                currentModel: 'test-model',
                status: 'ready',
                tools: [],
              }),
          },
        },
      ],
    }).compileComponents();
  });

  it('should render the control panel', async () => {
    const fixture = TestBed.createComponent(App);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();

    const compiled = fixture.nativeElement as HTMLElement;
    expect(compiled.textContent).toContain('Control panel');
    expect(compiled.textContent).toContain('FrostAgent');
  });
});
